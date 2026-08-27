// 引数なしで叩いたときの対話モード。フラグをチェックボックスで選び、llmtpl.conf を
// 書き換えて apply まで走らせる。
//
// **huh に触るのはこのファイルだけ**。v2 でモジュールパスが変わる（charm.land/huh/v2）ので、
// 差し替えをここに閉じ込める。判断の要る部分（変更計画・conf の書き換え）は
// internal/confedit の純関数へ出してあり、この層は「聞いて、並べて、渡す」だけに保つ。
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	xterm "github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/ryokwkm/llmtpl/internal/apply"
	"github.com/ryokwkm/llmtpl/internal/bundle"
	"github.com/ryokwkm/llmtpl/internal/confedit"
	"github.com/ryokwkm/llmtpl/internal/fileout"
	"github.com/ryokwkm/llmtpl/internal/flags"
	"github.com/ryokwkm/llmtpl/internal/msg"
)

// canceledErr は対話を人が中止したことを表す（exit 130 用）。
//
// **huh.ErrUserAborted をそのまま上へ流さない** —— 終了コードの判定に外部ライブラリの
// sentinel が混ざると、huh を差し替えたときに main が黙って壊れる。
type canceledErr struct{}

func (canceledErr) Error() string { return msg.M.Interactive.Canceled }

// prompter は対話で人に尋ねる 3 つの問い。
//
// **フローから切り離してあるのは差し替えのためではなくテストのため**。フォームは PTY を
// 要求するので、実装の中に埋めると「conf をどう書き換えて何を apply したか」を自動で
// 確かめる手段がまるごと無くなる（この 3 つを差し替えれば、残り全部が普通の関数になる）。
type prompter struct {
	// AskCreate は配下にターゲットが 1 つも無いとき、cwd への conf 作成の可否を尋ねる。
	AskCreate func(confPath string) (bool, error)
	// AskTarget は編集するターゲットを選ばせる（ターゲットが複数のときだけ呼ばれる）。
	// 戻りは groups の添字。「もう終わる」は esc（huh.ErrUserAborted）で表す。
	AskTarget func(groups []targetGroup) (int, error)
	// AskFlags は 1 ターゲット分の ON にするフラグ名を選ばせる。
	AskFlags func(g targetGroup) ([]string, error)
	// AskApply は conf の更新と apply の実行の可否を尋ねる。
	AskApply func() (bool, error)
}

// targetGroup はフォームの 1 区画（ターゲット 1 つ分）。
type targetGroup struct {
	r     resolved
	Title string // 表示名（cwd からの相対）
	Items []item
}

// runInteractive は引数なし実行の入口。TTY でなければ従来どおり help を出す。
func runInteractive(cmd *cobra.Command) error {
	// 非 TTY（パイプ・CI・リダイレクト）でフォームを立てると、応答の来ない画面で固まる。
	// help を返すのは cobra の従来挙動そのままで、既存のスクリプトを壊さないため
	if !isInteractive() {
		return cmd.Help()
	}
	return interactiveFlow(huhPrompter())
}

// interactiveFlow は対話モードの本体（尋ね方だけを外から受け取る）。
//
// 2 段方式: ターゲットが 1 つならそのままフラグ選択へ、複数なら
// 「ターゲットを選ぶ → そのフラグを選ぶ → 確認 → conf 書き換え → apply → 一覧へ戻る」を
// 終わるまで繰り返す。1 画面に全区画を並べる形は huh と相性が悪くやめた —— 矢印が区画を
// 跨げず（huh は端でクランプ）、enter が「次の区画へ / 確定」の二重の意味を持つ。
func interactiveFlow(p prompter) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	rs, err := resolveRoot(nil, "")
	if err != nil {
		return err // ルート未解決なら HomeNotFound が解決順ごと出る（= 状況の表示を兼ねる）
	}

	// 配下にターゲットが 1 つも無いときだけ、cwd への conf 作成を聞く。
	// 1 件でもあれば作成は挟まず、それを出す（親の階層に conf を増やす導線を作らない）
	if len(rs) == 0 {
		proceed, err := confirmCreate(p, dir, filepath.Join(dir, apply.TargetConfName))
		if err != nil {
			return err
		}
		if !proceed {
			// 断られたのは中止であって失敗ではない（exit 0 で静かに終える）
			fmt.Printf(msg.M.Interactive.CreateDeclined, apply.TargetConfName)
			return nil
		}
		root, label, err := rootForDir("", dir, map[string]apply.Root{})
		if err != nil {
			return err
		}
		rs = []resolved{{tg: apply.Target{Dir: dir}, root: root, label: label}}
	}

	groups, err := buildGroups(rs)
	if err != nil {
		return err
	}
	// どの棚にもバンドルが無ければ選ばせるものが無い。空のフォームは操作できないので、
	// 状況だけ伝えて終える
	if len(groups) == 0 {
		fmt.Println(msg.M.Cmd.NoBundles)
		return nil
	}

	// 1 件なら選択は要らない（リポジトリの中で叩く、最頻の形。従来と同じ挙動）
	if len(groups) == 1 {
		return editTarget(p, groups[0])
	}

	for {
		idx, err := p.AskTarget(groups)
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				// 一覧での esc は「終わる」。編集済みの分は適用済みなので中止（130）ではない
				return nil
			}
			return err
		}
		if err := editTarget(p, groups[idx]); err != nil {
			if _, ok := err.(canceledErr); ok {
				continue // 編集中の esc はそのターゲットの中止 → 一覧へ戻る
			}
			return err
		}
		// 書き換えの結果（実効値・conf の行の有無）を次の一覧と編集へ反映する
		if groups, err = buildGroups(rs); err != nil {
			return err
		}
	}
}

// editTarget は 1 ターゲット分の「フラグを選ぶ → 確認 → conf 書き換え → apply」。
func editTarget(p prompter, g targetGroup) error {
	confPath := filepath.Join(g.r.tg.Dir, apply.TargetConfName)
	confExists := fileExists(confPath)
	confText, err := readFile(confPath)
	if err != nil {
		return err
	}

	picked, err := p.AskFlags(g)
	if err != nil {
		return formErr(err)
	}
	changes := confedit.Plan(desiredOf(g.Items, picked), writtenFlags(g.Items), g.r.root.Defaults, g.r.root.Names())

	ok, err := confirmApply(p, changes, confExists)
	if err != nil {
		return err
	}
	if !ok {
		return canceledErr{}
	}
	if err := writeConf(confPath, confText, changes, confExists); err != nil {
		return err
	}

	// どの棚で合成したかがスクロールバックで追えるよう、apply の前にルートの行を出す
	fmt.Printf(msg.M.Cmd.BundleRootLine, g.r.root.Dir, g.r.label)
	rep, err := g.r.root.Apply(g.r.tg, apply.Options{})
	if err != nil {
		return err
	}
	printReport(g.r.tg, rep, false, false)
	return nil
}

// buildGroups は各ターゲットをフォームの区画へ組み立てる（バンドルの無い棚は区画ごと出さない）。
func buildGroups(rs []resolved) ([]targetGroup, error) {
	out := make([]targetGroup, 0, len(rs))
	for _, r := range rs {
		items, err := bundleItems(r.root, r.tg)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			continue
		}
		out = append(out, targetGroup{r: r, Title: rel(r.tg.Dir), Items: items})
	}
	return out, nil
}

// rootLine は区画の見出しに添えるバンドルルートの行（末尾の改行なし）。
func rootLine(r resolved) string {
	return strings.TrimSuffix(fmt.Sprintf(msg.M.Cmd.BundleRootLine, r.root.Dir, r.label), "\n")
}

// isInteractive は stdin / stdout の両方が端末かを返す。
// 片方でもパイプなら対話は成立しない（入力が来ない・画面制御が出力を汚す）。
func isInteractive() bool {
	return xterm.IsTerminal(os.Stdin.Fd()) && xterm.IsTerminal(os.Stdout.Fd())
}

// optionChrome は huh が 1 行の先頭へ付ける飾りの幅（カーソル "> " + 選択印 "[x] "）と、
// フォームの枠・余白の見積もり。**多めに取る** —— 1 桁でも溢れると折り返して
// viewport の高さ計算が崩れる（optionLabel の注記を参照）。
const optionChrome = 10

// labelWidth は 1 行に使ってよい表示幅。端末幅が取れなければ 80 桁とみなす。
func labelWidth() int {
	w, _, err := xterm.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		w = 80
	}
	return w - optionChrome
}

// huhPrompter は実際の端末で使う尋ね方（huh のフォーム）。
func huhPrompter() prompter {
	return prompter{
		AskCreate: func(confPath string) (bool, error) {
			return askConfirm(fmt.Sprintf(msg.M.Interactive.CreateConfirm, rel(confPath)))
		},
		AskTarget: askTarget,
		AskFlags:  askFlags,
		AskApply: func() (bool, error) {
			return askConfirm(fmt.Sprintf(msg.M.Interactive.ConfirmTitle, apply.TargetConfName))
		},
	}
}

// askConfirm ははい／いいえを 1 つ尋ねる。
func askConfirm(title string) (bool, error) {
	var ok bool
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Affirmative(msg.M.Interactive.Affirmative).
			Negative(msg.M.Interactive.Negative).
			Value(&ok),
	)).WithTheme(theme()).WithKeyMap(keymap()).Run()
	return ok, err
}

// keymap は既定に **esc での中止**を足したもの。
//
// huh の既定は中止が `ctrl+c` だけで、esc は割り当てが無い（フィルタ中のみフィルタ解除に使う）。
// 画面には「esc 中止」と出しているので、そのとおり効かないと嘘になる。フィルタは
// `Filterable(false)` で切ってあるので esc は空いている。
func keymap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	// Select の絞り込み（`/`）も切る。数件のターゲットに要らないうえ、フィルタ中は
	// huh が esc をフィルタ解除に使うので、生かすと esc が中止に効かない画面ができる
	km.Select.Filter = key.NewBinding(key.WithDisabled())
	return km
}

// theme は既定（ThemeCharm）の印を `[x]` / `[ ]` へ差し替えたもの。
//
// 既定は選択を `✓` の**色**で表すが、ON/OFF がひと目で分からない —— 色は
// 明るい端末や色覚特性で落ちるうえ、`✓` と `•` は形が近い。角括弧なら
// 中身の有無そのものが状態なので、色が無くても読める（色は補強として残す）。
func theme() *huh.Theme {
	t := huh.ThemeCharm()
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.SetString("[x] ")
	t.Focused.UnselectedPrefix = t.Focused.UnselectedPrefix.SetString("[ ] ")
	t.Blurred.SelectedPrefix = t.Blurred.SelectedPrefix.SetString("[x] ")
	t.Blurred.UnselectedPrefix = t.Blurred.UnselectedPrefix.SetString("[ ] ")
	return t
}

// confirmCreate は配下にターゲットが無いとき、cwd に conf を作ってよいかを確かめる。
//
// **ここではまだ作らない**（この後の選択で中止されるとゴミが残る）。承諾だけを取り、
// 書き込みは最終確認の後の writeConf が一手で行う。
func confirmCreate(p prompter, dir, confPath string) (bool, error) {
	fmt.Printf(msg.M.Interactive.NoConfHere, rel(dir), apply.TargetConfName)
	ok, err := p.AskCreate(confPath)
	if err != nil {
		return false, formErr(err)
	}
	return ok, nil
}

// item はチェックボックス 1 行分。
type item struct {
	Name      string
	Desc      string // bundle.conf の description（無ければ空）
	Effective bool   // いまの実効値 = 初期選択
	Written   bool   // llmtpl.conf に行があるか
	Current   bool   // その行の値（Written が false なら無意味）
	DefaultOn bool   // defaults.conf で ON か
}

// bundleItems は棚のバンドルを、実効値と説明付きの選択肢へ組み立てる（名前順）。
func bundleItems(root apply.Root, tg apply.Target) ([]item, error) {
	eff, err := root.Flags(tg) // conf の未知フラグはここで弾かれる
	if err != nil {
		return nil, err
	}
	// 実効値とは別に「conf に行として書かれているか」も要る —— 既定値で足りる分を
	// 書かずに済ませる（= 差分最小）判断が、実効値だけでは付かない
	conf, err := flags.ParseConf(filepath.Join(tg.Dir, apply.TargetConfName))
	if err != nil {
		return nil, err
	}

	out := make([]item, 0, len(root.Bundles))
	for _, b := range root.Bundles {
		meta, err := bundle.LoadMeta(b)
		if err != nil {
			return nil, err
		}
		cur, written := conf.Flags[b.Name]
		out = append(out, item{
			Name:      b.Name,
			Desc:      meta.Description,
			Effective: eff[b.Name],
			Written:   written,
			Current:   cur,
			DefaultOn: root.Defaults[b.Name],
		})
	}
	return out, nil
}

// askTarget は編集するターゲットを 1 つ選ばせる。
func askTarget(groups []targetGroup) (int, error) {
	form, idx := targetForm(groups, labelWidth())
	err := form.Run()
	return *idx, err
}

// targetForm はターゲット選択のフォームを組む（走らせない。分けてあるのはテストのため）。
func targetForm(groups []targetGroup, width int) (*huh.Form, *int) {
	opts := make([]huh.Option[int], 0, len(groups))
	for i, g := range groups {
		opts = append(opts, huh.NewOption(targetLabel(g, width), i))
	}
	var idx int
	// 絞り込みの無効化は keymap 側（下記 keymap 参照）。Select には MultiSelect の
	// Filterable(false) に相当する API が無く、`Filtering(false)` は「今フィルタ中か」の
	// 状態設定で別物（しかも filter.Focus() の副作用がある）
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title(msg.M.Interactive.TargetTitle).
			Options(opts...).
			Value(&idx),
	)).WithTheme(theme()).WithKeyMap(keymap())
	return form, &idx
}

// targetLabel は選択肢 1 行分（ターゲット名 + いま ON のフラグ）。
func targetLabel(g targetGroup, width int) string {
	on := make([]string, 0, len(g.Items))
	for _, it := range g.Items {
		if it.Effective {
			on = append(on, it.Name)
		}
	}
	summary := msg.M.Cmd.OnNone
	if len(on) > 0 {
		summary = strings.Join(on, ", ")
	}
	const sep = " — ON: "
	room := width - displayWidth(g.Title) - displayWidth(sep)
	if s := truncateWidth(summary, room); s != "" {
		return g.Title + sep + s
	}
	return g.Title
}

// askFlags は 1 ターゲット分のチェックボックスを出し、選ばれたフラグ名を返す。
//
// **通常画面で出す**（huh の既定のまま）。代替画面（tea.WithAltScreen）は一度入れて外した ——
// 「先頭のバンドルが消える」への対策のつもりだったが真因は初期選択の渡し方（selectForm 参照）で、
// 手前に出した行を隠す副作用だけが残ったため。
func askFlags(g targetGroup) ([]string, error) {
	form, picked := selectForm(g, labelWidth())
	err := form.Run()
	return *picked, err
}

// selectForm は 1 ターゲット分のチェックボックスのフォームを組む（走らせない）。
//
// **走らせる処理と分けてあるのはテストのため**。フォームは PTY 無しでは Run できないが、
// `Init` → `WindowSizeMsg` → `View` なら端末なしで描画結果を確かめられる。
// 「全バンドルが 1 画面に出る」は実機で 3 回落としているので、ここを見張る。
func selectForm(g targetGroup, width int) (*huh.Form, *[]string) {
	opts := make([]huh.Option[string], 0, len(g.Items))
	picked := make([]string, 0, len(g.Items))
	for _, it := range g.Items {
		// 🔴 **初期選択を Option.Selected() で渡してはいけない**。huh の `Options()` は
		// `selectOptions()` を呼び、そこが「最初に選択済みの項目」の**添字をそのまま
		// viewport の YOffset へ代入する**（field_multiselect.go:139-157）。つまり先頭が
		// OFF だとその分だけ一覧が上へずれ、**先頭のバンドルが画面から消える**。
		// 端末の広さとは無関係で、実機で agenttrail が消えた真因がこれ。
		opts = append(opts, huh.NewOption(optionLabel(it, width), it.Name))
		if it.Effective {
			picked = append(picked, it.Name)
		}
	}

	// **初期選択は Value() で渡す**。`Value` → `Accessor` は選択印を立てるだけで
	// cursor と YOffset に触らないので、一覧は先頭から出る。
	// ⚠️ **`Options()` より後に呼ぶこと**（先に呼ぶと `Options()` の `selectOptions()` が
	// 束縛済みの値を拾って、結局 YOffset を動かす）。
	//
	// **Height は指定しない**。指定しても YOffset は動かないので効かず、
	// 未指定なら選択肢の数に自動で合う。
	//
	// **Filterable(false)**: 絞り込みは要らず、切ると `/` と esc が空く。
	// esc を中止に使うために必要（huh は esc をフィルタの設定・解除に割り当てている）。
	form := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title(g.Title).
			Description(rootLine(g.r)).
			Options(opts...).
			Filterable(false).
			Value(&picked),
	).Title(msg.M.Interactive.SelectTitle)).WithTheme(theme()).WithKeyMap(keymap())
	return form, &picked
}

// desiredOf は「選ばれた名前」を全フラグ分の真偽値へ広げる。
// 選ばれなかったものを false で明示するのが要点 —— 母集合が欠けると Plan が
// 「触れられていないフラグ」を判別できない。
func desiredOf(items []item, picked []string) flags.Set {
	out := make(flags.Set, len(items))
	for _, it := range items {
		out[it.Name] = false
	}
	for _, name := range picked {
		out[name] = true
	}
	return out
}

// optionLabel は 1 行の表示。説明は bundle.conf 由来のデータなので、UI の言語には従わない。
//
// 🔴 **width に収めて折り返させないこと**。huh は 1 オプションを 1 行として viewport の高さを
// 決めるので、端末で折り返すと実際の表示行数が計算を超え、**先頭のバンドルが画面の外へ押し出される**
// （2026-08-26 に実機で agenttrail が消えて発覚した）。
//
// 削る順序は「説明 → 何も削らない」。**フラグ名と既定 ON の注記は削らない** ——
// 名前が切れると何を選んでいるか分からず、注記が消えると
// 「外すと `name = false` の明示追記が起きる」理由が選ぶ前に見えなくなる。
func optionLabel(it item, width int) string {
	head := it.Name
	tail := ""
	if it.DefaultOn {
		tail = " " + msg.M.Interactive.DefaultOnNote
	}
	if it.Desc == "" {
		return head + tail
	}

	const sep = " — "
	room := width - displayWidth(head) - displayWidth(sep) - displayWidth(tail)
	desc := truncateWidth(it.Desc, room)
	if desc == "" {
		return head + tail // 説明を置く余地が無い（名前と注記は残す）
	}
	return head + sep + desc + tail
}

// truncateWidth は表示幅が max を超えないよう末尾を … で詰める（全角を 2 と数える）。
func truncateWidth(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if displayWidth(s) <= max {
		return s
	}
	const ellipsis = "…"
	budget := max - displayWidth(ellipsis)
	if budget <= 0 {
		return ""
	}
	w := 0
	for i, r := range s {
		rw := 1
		if isWideRune(r) {
			rw = 2
		}
		if w+rw > budget {
			return s[:i] + ellipsis
		}
		w += rw
	}
	return s + ellipsis
}

// writtenFlags は「llmtpl.conf に行として書かれているフラグ」だけを返す（Plan の入力）。
func writtenFlags(items []item) flags.Set {
	out := flags.Set{}
	for _, it := range items {
		if it.Written {
			out[it.Name] = it.Current
		}
	}
	return out
}

// confirmApply は変更内容を印字してから、conf の更新と apply の実行を確かめる。
//
// 差分は huh の中ではなく **先に stdout へ出す**。フォームは終了時に画面を畳むので、
// 中に入れると「何を承諾したか」がスクロールバックに残らない。
func confirmApply(p prompter, changes []confedit.Change, confExists bool) (bool, error) {
	switch {
	case len(changes) == 0 && confExists:
		fmt.Printf(msg.M.Interactive.NoChanges, apply.TargetConfName)
	case len(changes) == 0:
		// 「作りますか？→ はい」で全部が既定のままだった場合。目印としての conf は作るので、
		// 「書き換えません」とは言わない（この後に「作成」と出るので矛盾する）
		fmt.Printf(msg.M.Interactive.NoChangesNew, apply.TargetConfName)
	default:
		fmt.Print(msg.M.Interactive.Changes)
		for _, c := range changes {
			if c.Old == nil {
				fmt.Printf(msg.M.Interactive.ChangeAppend, c.Name, boolText(c.New))
				continue
			}
			fmt.Printf(msg.M.Interactive.ChangeReplace, c.Name, boolText(*c.Old), boolText(c.New))
		}
	}

	ok, err := p.AskApply()
	if err != nil {
		return false, formErr(err)
	}
	return ok, nil
}

// writeConf は conf を書き換える。変更が無くても**ファイルが無ければ作る** ——
// llmtpl.conf の存在がターゲットの唯一の目印なので、「作りますか？→ はい」の結果は
// 中身が空でも残さないと、次に叩いたときまた同じ質問になる。
func writeConf(confPath, src string, changes []confedit.Change, confExists bool) error {
	out, err := confedit.Rewrite(src, changes)
	if err != nil {
		return err
	}
	if out == src && confExists {
		return nil
	}
	// 生成物ではないので fileout.Write は通さない（あちらは GENERATED マーカーの無い
	// ファイルを手書きとみなして .archive へ退避する。conf はまさにその手書き）
	if err := fileout.WriteAtomic(confPath, []byte(out), 0o644); err != nil {
		return err
	}
	line := msg.M.Interactive.WroteConf
	if !confExists {
		line = msg.M.Interactive.CreatedConf
	}
	fmt.Printf(line, rel(confPath))
	return nil
}

// formErr は huh の中止を自前の sentinel へ変換する（それ以外はそのまま）。
func formErr(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return canceledErr{}
	}
	return err
}

// readFile は conf を読む。無ければ空文字（新規作成の経路がそのまま乗る）。
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
