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
	// AskCreate は conf の無いディレクトリで作成の可否を尋ねる。subTargets は配下のターゲット数。
	AskCreate func(confPath string, subTargets int) (bool, error)
	// AskFlags は ON にするフラグ名を選ばせる。
	AskFlags func(items []item) ([]string, error)
	// AskApply は conf の更新と apply の実行の可否を尋ねる。
	AskApply func() (bool, error)
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
func interactiveFlow(p prompter) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	confPath := filepath.Join(dir, apply.TargetConfName)
	confExists := fileExists(confPath)

	// conf が無い場所は「ターゲットではない」。作れば対話を続けられるので、状況を見せて聞く
	if !confExists {
		proceed, err := confirmCreate(p, dir, confPath)
		if err != nil {
			return err
		}
		if !proceed {
			// 断られたのは中止であって失敗ではない（exit 0 で静かに終える）
			fmt.Printf(msg.M.Interactive.CreateDeclined, apply.TargetConfName)
			return nil
		}
	}

	targets, root, src, err := resolveRoot(nil, "")
	if err != nil {
		return err // ルート未解決なら HomeNotFound が解決順ごと出る（= 状況の表示を兼ねる）
	}
	fmt.Printf(msg.M.Cmd.BundleRootLine, root.Dir, src)

	tg := apply.Target{Dir: dir}
	items, err := bundleItems(root, tg)
	if err != nil {
		return err
	}
	confText, err := readFile(confPath)
	if err != nil {
		return err
	}

	picked, err := p.AskFlags(items)
	if err != nil {
		return formErr(err)
	}
	changes := confedit.Plan(desiredOf(items, picked), writtenFlags(items), root.Defaults, root.Names())

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

	// **apply は cwd の 1 ターゲットだけに絞る**。runApply / resolveScope を通すと
	// DiscoverTargets が配下へ再帰し、いま編集していない子ターゲットまで再生成してしまう
	rep, err := root.Apply(tg, apply.Options{})
	if err != nil {
		return err
	}
	printReport(tg, rep, false, false)

	// cwd を引いて数える。**len(targets)-1 では足りない** —— conf を今から作る経路では
	// resolveRoot の時点でファイルがまだ無く、cwd 自身が targets に入っていない
	others := 0
	for _, t := range targets {
		if t.Dir != dir {
			others++
		}
	}
	if others > 0 {
		fmt.Printf(msg.M.Interactive.OtherTargetsHint, others)
	}
	return nil
}

// isInteractive は stdin / stdout の両方が端末かを返す。
// 片方でもパイプなら対話は成立しない（入力が来ない・画面制御が出力を汚す）。
func isInteractive() bool {
	return xterm.IsTerminal(os.Stdin.Fd()) && xterm.IsTerminal(os.Stdout.Fd())
}

// optionChrome は huh が 1 行の先頭へ付ける飾りの幅（カーソル "> " + 選択印 "✓ "）と、
// フォームの枠・余白の見積もり。**多めに取る** —— 1 桁でも溢れると折り返して
// viewport の高さ計算が崩れる（optionLabel の注記を参照）。
const optionChrome = 8

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
		AskCreate: func(confPath string, subTargets int) (bool, error) {
			// ここに conf を作ると、この階層自体がターゲットになる。配下に既にターゲットがある
			// （＝ 設定リポジトリのルートのような場所）なら、まず意図を疑うべきなので見せる
			if subTargets > 0 {
				fmt.Printf(msg.M.Interactive.SubTargetsWarn, subTargets)
			}
			return askConfirm(fmt.Sprintf(msg.M.Interactive.CreateConfirm, rel(confPath)))
		},
		AskFlags: askFlags,
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
	)).Run()
	return ok, err
}

// confirmCreate は conf の無いディレクトリで、作ってよいかを確かめる。
//
// **ここではまだ作らない**（この後の選択で中止されるとゴミが残る）。承諾だけを取り、
// 書き込みは最終確認の後の writeConf が一手で行う。
func confirmCreate(p prompter, dir, confPath string) (bool, error) {
	fmt.Printf(msg.M.Interactive.NoConfHere, rel(dir), apply.TargetConfName)

	sub := 0
	if found, err := apply.DiscoverTargets(dir); err == nil {
		sub = len(found)
	}
	ok, err := p.AskCreate(confPath, sub)
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

// askFlags はチェックボックスを出し、選ばれたフラグ名を返す。
func askFlags(items []item) ([]string, error) {
	w := labelWidth()
	opts := make([]huh.Option[string], 0, len(items))
	for _, it := range items {
		opts = append(opts, huh.NewOption(optionLabel(it, w), it.Name).Selected(it.Effective))
	}

	// Height は指定しない。未設定なら huh が選択肢の数に合わせて全部を 1 画面へ収める
	// （固定値を渡すと title/description の分を引いた上で下限に丸められ、狭い端末で崩れる）
	var picked []string
	err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title(msg.M.Interactive.SelectTitle).
			Description(msg.M.Interactive.SelectHelp).
			Options(opts...).
			Value(&picked),
	)).Run()
	return picked, err
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
