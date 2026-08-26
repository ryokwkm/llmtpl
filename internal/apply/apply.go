// Package apply は「バンドルルート → ターゲット」の適用をオーケストレーションする。
//
// 流れ:
//  1. LoadRoot でバンドルルート（llm-tpl/）を 1 回だけ読む（バンドル一覧・既定フラグ）
//  2. Root.Flags でターゲットの実効フラグを決める（defaults → llmtpl.conf の後勝ち）
//  3. Root.Apply が ON バンドルの断片を合成し、生成物を原子的に書き出す
//  4. 続けて link.Sync でディレクトリ（rules / skills / …）のリンクを合わせる
//
// 純粋な処理（テンプレ評価・slot 合成・JSON マージ・書き出し）は internal/render・
// internal/bundle・internal/mergejson・internal/fileout に分かれており、このパッケージは
// 「どれを読んでどこへ書くか」を決める層。表示と終了コードは main の責務。
package apply

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ryokwkm/llmtpl/internal/bundle"
	"github.com/ryokwkm/llmtpl/internal/fileout"
	"github.com/ryokwkm/llmtpl/internal/flags"
	"github.com/ryokwkm/llmtpl/internal/link"
	"github.com/ryokwkm/llmtpl/internal/mergejson"
	"github.com/ryokwkm/llmtpl/internal/render"
	"github.com/ryokwkm/llmtpl/internal/state"

	"github.com/ryokwkm/llmtpl/internal/msg"
)

const (
	// BundleDirName は親を辿って探すバンドルルートのディレクトリ名。
	BundleDirName = "llm-tpl"
	// TargetConfName はターゲットのフラグ上書きファイル。
	TargetConfName = "llmtpl.conf"
	// DefaultsConfName はバンドルルートに置く既定フラグ。
	DefaultsConfName = "defaults.conf"
	// EnvHome は バンドルルートを明示する環境変数。
	EnvHome = "LLMTPL_HOME"
	// GeneratedMarker は Markdown 生成物の先頭行に入れる「直接編集禁止」マーカ。
	// HTML コメントは Claude Code の context 注入前に除去されるためトークンを消費しない。
	GeneratedMarker = "<!-- GENERATED"

	headerFormat = GeneratedMarker + " — 直接編集禁止。原本: %s（編集は原本 → llmtpl apply で反映） -->"

	archiveDirName = ".archive"
)

// Options は実行時の設定（バンドルルートに依存しないもの）。
type Options struct {
	DryRun     bool
	ArchiveDir string // 空ならターゲット直下の .archive
}

// Target は 1 つのターゲット = **プロジェクトルート**。
// 入力（llmtpl.conf・*.tmpl・partials/）も出力（生成物・リンク）もこの配下に出る。
//
// バンドルの中身はここへそのまま重なるので、<flag>/.claude/rules は <T>/.claude/rules へ、
// <flag>/AGENTS.md.tmpl は <T>/AGENTS.md へ届く。
//
// **層の名前は llmtpl の知識に持たない**。ターゲット側は自分が持つドット始まりディレクトリを
// 数え（targetLayers）、リンクの走査層は棚のバンドルから集める（bundle.Layers）。
// `.claude` は「バンドルが 1 つも無くても掃除できるように」既定へ入れてあるだけ。
type Target struct {
	Dir string
}

// TargetReport は生成物 1 つの結果。
type TargetReport struct {
	Dest         string // 生成先
	Changed      bool   // 内容が変わった（DryRun なら変わる予定）
	Archived     string // 生成物でない実体を退避したパス
	WouldArchive bool   // DryRun 時: 退避が必要
	Skipped      string // 空でなければスキップ理由
}

// Orphan は ON バンドルが持っているのに、ターゲットに受け皿（同名の *.tmpl）が無い断片。
// 差し込み先が存在しないので、この断片はどこにも入らない。
type Orphan struct {
	Bundle string // 断片を持っているバンドル名
	File   string // 断片のファイル名（例: CLAUDE.md.tmpl）
}

// Report は 1 ターゲットの適用結果。
type Report struct {
	On      []string // ON のバンドル名（ソート順）
	Targets []TargetReport
	Links   []link.Action // rules / skills / agents / commands / hooks のリンク合成結果
	Orphans []Orphan      // 受け皿が無くて届かなかった断片
}

// Diffs は「apply で解消される差分」の件数。check の終了コード判定はこれを使う
// （表示側の都合で判定が変わらないよう、データ層で決める）。
// 名前衝突（link.KindConflict）は apply では解消しない設定ミスなので数えない（表示で常に警告する）。
func (r Report) Diffs() int {
	n := 0
	for _, t := range r.Targets {
		if t.Changed {
			n++
		}
	}
	for _, l := range r.Links {
		switch l.Kind {
		case link.KindCreated, link.KindRemoved, link.KindArchived:
			n++
		}
	}
	return n
}

// Root はバンドルルートから 1 回だけ読む不変の情報。
// ターゲットごとに読み直さないため、-r で N 個回しても I/O は 1 回で済む。
type Root struct {
	Dir      string
	Bundles  []bundle.Bundle // 名前のソート順
	Known    map[string]bool // バンドル名の集合（フラグと slot 名の検証用）
	Defaults flags.Set       // defaults.conf
	// Layers は棚にあるバンドルが使うオーバーレイ層の和（.claude を必ず含む）。
	// **ON/OFF を問わず棚全体から集める** —— OFF にしたバンドルの層も走査対象に
	// 残さないと、その層へ配ったリンクを外せなくなる。
	Layers []string
}

// LoadRoot はバンドルルートを読む。
func LoadRoot(dir string) (Root, error) {
	bundles, err := bundle.Discover(dir)
	if err != nil {
		return Root{}, err
	}
	known := make(map[string]bool, len(bundles))
	for _, b := range bundles {
		// バンドル名は「ドット始まり以外のディレクトリ名」を無条件に採るので、予約キーと
		// 同名のディレクトリを作れてしまう。作られると conf の `bundle_root = true` を
		// パーサが横取りするため **そのバンドルは永久に ON にできず、known に名前があるので
		// Validate も何も言わない**（静かな死）。ここで殺す
		if flags.IsReserved(b.Name) {
			return Root{}, fmt.Errorf(msg.M.Apply.ReservedBundleName, b.Dir)
		}
		known[b.Name] = true
	}

	layers, err := bundle.Layers(bundles)
	if err != nil {
		return Root{}, err
	}

	defaultsPath := filepath.Join(dir, DefaultsConfName)
	defaults, err := flags.ParseConf(defaultsPath)
	if err != nil {
		return Root{}, err
	}
	// defaults.conf はバンドルルートの中にあるので、そこでルートを指定するのは定義上「遅すぎる」。
	// 黙って無視すると書いた本人が永久に気づけないので落とす
	if defaults.BundleRoot != "" {
		return Root{}, fmt.Errorf(msg.M.Apply.BundleRootInDefaults,
			defaultsPath, defaults.BundleRootLine, flags.KeyBundleRoot, DefaultsConfName, EnvHome, TargetConfName)
	}
	if err := flags.Validate(defaults.Flags, known, defaultsPath); err != nil {
		return Root{}, err
	}

	return Root{Dir: dir, Bundles: bundles, Known: known, Defaults: defaults.Flags, Layers: layers}, nil
}

// Names はバンドル名の一覧（ソート済み）。
func (r Root) Names() []string { return bundle.Names(r.Bundles) }

// Flags はターゲットの実効フラグ。母集合（= 全バンドル名）を false で敷いた上に
// defaults.conf → 消費側 llmtpl.conf を後勝ちで重ねる。未設定は OFF。
//
// **母集合を敷くのが要点**: これが無いと、存在するのに conf へ書かれていないバンドルを
// テンプレが {{if .name}} で参照した瞬間に missingkey=error で apply 全体が落ちる
// （バンドル断片が他フラグを見るケースで実際に踏んだ）。タイポ検出は保たれる —
// 存在しないバンドル名は母集合にも入らないので、依然 missingkey で落ちる。
func (r Root) Flags(c Target) (flags.Set, error) {
	path := filepath.Join(c.Dir, TargetConfName)
	conf, err := flags.ParseConf(path)
	if err != nil {
		return nil, err
	}
	if err := flags.Validate(conf.Flags, r.Known, path); err != nil {
		return nil, err
	}
	base := make(flags.Set, len(r.Known))
	for name := range r.Known {
		base[name] = false
	}
	return flags.Merge(flags.Merge(base, r.Defaults), conf.Flags), nil
}

// on は ON のバンドル（Bundles の順序 = 名前順を保つ）。
func (r Root) on(eff flags.Set) []bundle.Bundle {
	out := make([]bundle.Bundle, 0, len(r.Bundles))
	for _, b := range r.Bundles {
		if eff[b.Name] {
			out = append(out, b)
		}
	}
	return out
}

// checkSlot は消費側テンプレの受け口 {{slot "name"}} の name がフラグ名（= バンドル名）で
// あることを確かめる。**差し込み先はフラグ名そのもの**なので独立した語彙ファイルは要らず、
// タイポは「そんなバンドルは無い」として弾ける（旧 slots.conf は廃止）。
func (r Root) checkSlot(slot, srcPath string) error {
	if slot == "" {
		return nil
	}
	if !r.Known[slot] {
		return fmt.Errorf(msg.M.Apply.UnknownSlot,
			srcPath, slot, strings.Join(r.Names(), ", "))
	}
	return nil
}

// checkNotBundle はバンドル配下をターゲットとして扱うのを防ぐ。
// バンドル断片（llm-tpl/<flag>/CLAUDE.md.tmpl）も「*.tmpl を持つディレクトリ」の条件を満たすため、
// 直接指定されると自分自身へ生成してしまう（-r 探索では llm-tpl/ へ降りないので起きない）。
func (r Root) checkNotBundle(dir string) error {
	// ルート自身も弾く（Under は自身を含まないので明示的に見る）
	if !Under(r.Dir, dir) && filepath.Clean(dir) != filepath.Clean(r.Dir) {
		return nil
	}
	return fmt.Errorf(msg.M.Apply.TargetUnderRoot, dir, r.Dir)
}

// Under は path が base の配下（base 自身は含まない）かを返す。
//
// 同じ述語が「文字列が .. で始まるか」で書かれていた箇所があり、`..foo` のような**名前**にも
// 一致していた。セパレータまで見ること。Rel が失敗する組（片方だけ相対など）は判定不能なので
// **安全側 = 配下とみなす**に倒す。呼び出し側はいずれも「配下なら弾く／除外する」ガードで、
// 判定不能を「配下でない」に倒すと fail-open になる。
func Under(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return true
	}
	if rel == "." {
		return false // base 自身は配下ではない
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Apply は 1 ターゲットへバンドルを適用する。
func (r Root) Apply(c Target, o Options) (Report, error) {
	var rep Report

	// 移行ガード: conf がオーバーレイ層に残っていると、ターゲット直下に conf が無いために
	// **全フラグ OFF で黙って生成**され、CLAUDE.md と settings.json が薄い内容で上書きされる。
	for _, ov := range targetLayers(c.Dir) {
		if isFile(filepath.Join(c.Dir, ov, TargetConfName)) {
			return rep, fmt.Errorf(msg.M.Apply.NotReadAtThisPath,
				filepath.Join(c.Dir, ov, TargetConfName), filepath.Join(c.Dir, TargetConfName))
		}
	}
	// ターゲットの basename がオーバーレイ層名 = 1 階層下から叩いた誤り。
	// そのまま通すと conf が見つからず全フラグ OFF になる。
	if strings.HasPrefix(filepath.Base(c.Dir), ".") {
		return rep, fmt.Errorf(msg.M.Apply.IsOverlayLayer,
			c.Dir, filepath.Dir(c.Dir))
	}

	eff, err := r.Flags(c)
	if err != nil {
		return rep, err
	}
	if err := r.checkNotBundle(c.Dir); err != nil {
		return rep, err
	}
	on := r.on(eff)
	for _, b := range on {
		// 旧レイアウトのバンドル（直下に rules/ 等）は移行先を名指しして落とす。
		// 黙って無視すると、指示だけ届かない静かな部分適用になる。
		if err := bundle.CheckLayout(b); err != nil {
			return rep, err
		}
		rep.On = append(rep.On, b.Name)
	}

	// 退避先はターゲット直下。ターゲットの外（バンドルルート側）へ出すと、バンドルが
	// 別リポジトリにあるとき手編集の退避物が無関係なリポジトリの git 管理下へ落ちる。
	archiveDir := o.ArchiveDir
	if archiveDir == "" {
		archiveDir = filepath.Join(c.Dir, archiveDirName)
	}

	tmpls, err := targetTemplates(c.Dir)
	if err != nil {
		return rep, err
	}

	// JSON 生成物は先頭にマーカを書けないので、前回書いた内容のハッシュで「自分の生成物か」を判定する。
	// state はターゲットのルート直下に置く（キーはターゲット相対パス）
	st, migrated, err := loadState(c.Dir, tmpls)
	if err != nil {
		return rep, err
	}

	x := &run{root: r, c: c, o: o, eff: eff, on: on, archiveDir: archiveDir, st: st,
		partialDirs: partialDirsFor(c, on)}

	if rep.Orphans, err = orphans(on, tmpls); err != nil {
		return rep, err
	}
	hashes := map[string]string{} // 今回の生成物のハッシュ（state に残す分）
	for _, tmpl := range tmpls {
		tr, err := x.target(tmpl, hashes)
		if err != nil {
			return rep, err
		}
		rep.Targets = append(rep.Targets, tr)
	}

	// state は「今回の生成物」だけを残す（消えた *.tmpl のエントリは prune される）。
	// migrated なら中身が同じでも必ず書く —— 書かないと新しい置き場にファイルが出来ず、
	// 次回また旧位置からのフォールバックに頼ることになる。
	if !o.DryRun && (migrated || !maps.Equal(st.Generated, hashes)) {
		st.Version = state.Version // 旧 version を読んだ場合に上げ忘れない
		st.Generated = hashes
		if err := state.Save(c.Dir, st); err != nil {
			return rep, err
		}
	}

	// ディレクトリ（rules / skills / agents / commands / hooks …）のリンク合成。
	// 生成物の書き出しが全部成功してから行う（半端な状態を作らない）。
	rep.Links, err = link.Sync(c.Dir, on, link.Options{
		TplHome:    r.Dir,
		ArchiveDir: archiveDir,
		DryRun:     o.DryRun,
		// 所有リンクを探す層は棚全体から集めたもの（すべてドット始まり）。
		// ドット始まりであることが安全弁で、層が "." になると削除範囲がターゲット全体へ広がる。
		ScanLayers: r.Layers,
	})
	if err != nil {
		return rep, err
	}
	return rep, nil
}

// orphans は ON バンドルが持つ断片のうち、ターゲットに受け皿（同名の *.tmpl）が無いものを返す。
//
// **これを黙って捨てると最悪の部分適用になる** — ディレクトリ（skills / hooks）と settings.json は
// 配られるのに、その使い方を書いた CLAUDE.md 断片だけが届かない状態が exit 0 で成立する。
// 「フラグ 1 行で指示と実体が同時に増減する」という前提が、名前の一致というただ 1 点で静かに崩れる。
//
// **Diffs には数えない**。apply を何度実行しても解消しない（解消するのは人がターゲットへ
// *.tmpl を置いたとき）ので、数えると check が永久に exit 2 になり自動化が止まる。
func orphans(on []bundle.Bundle, tmpls []string) ([]Orphan, error) {
	// 照合は**相対パス**。バンドル側の断片も相対パスで返るので同じ空間で突き合わせられる。
	// basename 照合だと <flag>/AGENTS.md.tmpl と <flag>/.claude/AGENTS.md.tmpl が衝突していた。
	have := make(map[string]bool, len(tmpls))
	for _, t := range tmpls {
		have[t] = true
	}
	var out []Orphan
	for _, b := range on {
		frags, err := bundle.Fragments(b)
		if err != nil {
			return nil, err
		}
		for _, f := range frags {
			if !have[f] {
				out = append(out, Orphan{Bundle: b.Name, File: f})
			}
		}
	}
	return out, nil
}

// loadState は state を読む。ターゲットがプロジェクトルートへ上がった移行のため、
// 新しい置き場が空なら旧位置（<T>/<overlay>/）から読んでキーを相対パスへ復元する。
//
// 復元は**実在する受け口の位置から逆算**する。一律に <overlay>/ を足すのは誤りで、
// ターゲット直下の受け口（AGENTS.md 等）を取り違える。basename が複数の受け口に一致したら
// 復元不能としてそのキーだけ落とす —— 取り違えるより、その生成物が 1 回退避されるほうが安全。
func loadState(dir string, tmpls []string) (st state.State, migrated bool, err error) {
	st, err = state.Load(dir)
	if err != nil || len(st.Generated) > 0 {
		return st, false, err
	}

	byBase := map[string]string{} // basename → ターゲット相対の生成物名
	dup := map[string]bool{}
	for _, t := range tmpls {
		name := strings.TrimSuffix(t, ".tmpl")
		b := filepath.Base(name)
		if _, ok := byBase[b]; ok {
			dup[b] = true
			continue
		}
		byBase[b] = name
	}

	for _, ov := range targetLayers(dir) {
		old, err := state.Load(filepath.Join(dir, ov))
		if err != nil {
			return st, false, err
		}
		if len(old.Generated) == 0 {
			continue
		}
		restored := state.State{Version: state.Version, Generated: map[string]string{}}
		for base, hash := range old.Generated {
			if name, ok := byBase[base]; ok && !dup[base] {
				restored.Generated[name] = hash
			}
		}
		return restored, true, nil
	}
	return st, false, nil
}

// run はターゲット 1 件分の作業状態（生成物ごとに作り直さない値をまとめて持つ）。
type run struct {
	root        Root
	c           Target
	o           Options
	eff         flags.Set
	on          []bundle.Bundle
	partialDirs []string
	archiveDir  string
	st          state.State
}

// target は生成物 1 つを作って書き出す。種類ごとに違うのは「合成の仕方」と
// 「生成物かどうかの判定材料」だけで、書き出し・報告は 1 か所に集約する。
func (x *run) target(tmplRel string, hashes map[string]string) (TargetReport, error) {
	tmplPath := filepath.Join(x.c.Dir, tmplRel)
	name := strings.TrimSuffix(tmplRel, ".tmpl") // ターゲット相対のまま保つ
	base := filepath.Base(name)
	tr := TargetReport{Dest: filepath.Join(x.c.Dir, name)}
	opts := fileout.Options{
		ArchiveDir: x.archiveDir,
		// 退避先がターゲット単位なので、ラベルはターゲット相対パスだけで一意になる
		ArchiveLabel: flatten(name),
		DryRun:       x.o.DryRun,
	}

	var content []byte
	var err error
	trackHash := false
	switch {
	// settings.local.json（*.local.json）は 2026-08-20 に生成対象へ変えた。Claude Code が
	// /model・/permissions 等で書き込むライブファイルだが、生成物にするか（= 実行中の書き込みを
	// 次回 apply で退避し宣言を正とするか）は .tmpl を置くターゲット作者の判断で、置かなければ
	// 従来どおり触らない。手書き運用では「フラグを OFF に戻しても permission が残る」取り違えが
	// 構造的に起きるため、宣言的管理を opt-in で選べるようにした（グローバル settings.json を
	// 非目標から外した 2026-07-30 と同型の転換。経緯と代償は設計 doc §5.3 / §10）
	case strings.Contains(base, ".local.") && strings.HasSuffix(base, ".md") && filepath.Dir(name) != ".":
		// Claude Code の Local instructions は <repo>/CLAUDE.local.md だけで、
		// <repo>/.claude/CLAUDE.local.md は読まれない。読まれない生成物を黙って作らず loud に落とす。
		// **判定材料はその生成物の実出力先**（ターゲット直下かどうか）であって、ターゲットの名前ではない。
		// settings.local.json はこの逆（.claude/ 配下が読まれる位置）なので .md に限定する。
		return tr, fmt.Errorf(msg.M.Apply.NotGeneratedUnderLayer,
			base, filepath.Dir(name), filepath.Base(tmplPath), tmplPath)
	case strings.HasSuffix(base, ".md"):
		content, err = x.markdown(tmplRel)
		opts.GeneratedMarker = GeneratedMarker
	case strings.HasSuffix(base, ".json"):
		content, err = x.json(tmplRel)
		opts.KnownHash = x.st.Get(name)
		trackHash = true
	default:
		tr.Skipped = msg.M.Apply.SkippedUnsupported
		return tr, nil
	}
	if err != nil {
		return tr, err
	}

	res, err := fileout.Write(tr.Dest, content, opts)
	if err != nil {
		return tr, err
	}
	tr.Changed, tr.Archived, tr.WouldArchive = res.Changed, res.Archived, res.WouldArchive
	if trackHash {
		hashes[name] = fileout.Hash(content)
	}
	return tr, nil
}

// markdown は ON バンドルの断片を slot 別に合成し、消費側テンプレへ流し込む。
// 受け口が無かった寄稿は生成物の末尾へ追記する。
func (x *run) markdown(tmplRel string) ([]byte, error) {
	tmplPath := filepath.Join(x.c.Dir, tmplRel)
	var pieces []bundle.Piece
	err := x.eachFragment(tmplRel, func(b bundle.Bundle, fragPath string, frag []byte) error {
		res, err := x.render(fragPath, frag, nil)
		if err != nil {
			return err
		}
		pieces = append(pieces, bundle.Piece{Bundle: b.Name, Text: string(res.Content)})
		return nil
	})
	if err != nil {
		return nil, err
	}

	comp := bundle.Compose(pieces)
	res, err := x.render(tmplPath, nil, comp.Slots)
	if err != nil {
		return nil, err
	}

	// 消費側テンプレが使った slot 名も語彙で検証する（受け口側のタイポを弾く）
	for _, slot := range slices.Sorted(maps.Keys(res.UsedSlots)) {
		if err := x.root.checkSlot(slot, tmplPath); err != nil {
			return nil, err
		}
	}
	// 受け口が無い寄稿はエラーにせず末尾へ追記する（全ターゲットに全フラグの受け口を強制しないため）。
	// 追記が複数あるときの順序は**バンドル名の昇順**（受け口が無いので書き順が存在せず、
	// 決定的な規則を置かないと生成物が不安定になる）。
	content := res.Content
	for _, name := range slices.Sorted(maps.Keys(comp.Slots)) {
		if res.UsedSlots[name] {
			continue
		}
		content = appendFragment(content, comp.Slots[name])
	}
	return content, nil
}

// appendFragment は受け口が無かった断片を生成物の末尾へ足す。
// 断片は「先頭 \n・末尾改行なし」なので、末尾の改行を落としてから繋いで改行を戻すと
// {{- slot}} を末尾に置いた場合とバイト列が一致する。
func appendFragment(content []byte, frag string) []byte {
	var b bytes.Buffer
	b.Write(bytes.TrimRight(content, "\n"))
	b.WriteString(frag)
	b.WriteByte('\n')
	return b.Bytes()
}

// json は ON バンドルの JSON 断片を deep merge し、最後に消費側を重ねる（具体的な方が勝つ）。
// 適用順はバンドル名順（JSON には順序の指定を書けないので、決定的な規則を置く）。
func (x *run) json(tmplRel string) ([]byte, error) {
	tmplPath := filepath.Join(x.c.Dir, tmplRel)
	merged := map[string]any{}
	parse := func(path string, source []byte) error {
		res, err := x.render(path, source, nil)
		if err != nil {
			return err
		}
		obj, err := mergejson.Parse(res.Content, path)
		if err != nil {
			return err
		}
		merged, err = mergejson.Merge(merged, obj, path)
		return err
	}

	if err := x.eachFragment(tmplRel, func(_ bundle.Bundle, fragPath string, frag []byte) error {
		return parse(fragPath, frag)
	}); err != nil {
		return nil, err
	}
	if err := parse(tmplPath, nil); err != nil { // Source=nil でファイルから読む
		return nil, err
	}
	return mergejson.Format(merged)
}

// eachFragment は ON バンドルのうち、消費側テンプレと**同じ相対位置**の断片を持つものを順に渡す。
// 断片の探索規約（バンドル内の同じ相対パス）を知る場所を 1 つに保つ。
//
// バンドルの中身はターゲットの中へそのまま重なるので、照合は相対パスで行う。
// <T>/.claude/CLAUDE.md.tmpl の受け口には <flag>/.claude/CLAUDE.md.tmpl が、
// <T>/AGENTS.md.tmpl には <flag>/AGENTS.md.tmpl が届く。
func (x *run) eachFragment(tmplRel string, fn func(b bundle.Bundle, fragPath string, content []byte) error) error {
	for _, b := range x.on {
		frag, ok, err := bundle.LoadFragment(b, tmplRel)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := fn(b, filepath.Join(b.Dir, tmplRel), frag); err != nil {
			return err
		}
	}
	return nil
}

// render は 1 回のテンプレ評価。Header は消費側テンプレ（slots 付き）のときだけ付ける。
func (x *run) render(path string, source []byte, slots map[string]string) (render.Result, error) {
	o := render.Options{
		TmplPath:    path,
		Source:      source,
		PartialDirs: x.partialDirs,
		Flags:       x.eff,
		Slots:       slots,
	}
	if slots != nil && strings.HasSuffix(path, ".md.tmpl") {
		o.Header = fmt.Sprintf(headerFormat, x.headerLabel(path))
	}
	return render.Render(o)
}

// headerLabel は GENERATED ヘッダに出す原本パス。**cwd に依存させない**
// （依存させると実行ディレクトリで生成物のバイト列が変わり、別 cwd からの check が常に差分を報告する）。
// 基準はバンドルルートの親（= 設定リポジトリのルート）→ ターゲット → basename の順。
func (x *run) headerLabel(tmplPath string) string {
	for _, base := range []string{filepath.Dir(x.root.Dir), x.c.Dir} {
		if rel, err := filepath.Rel(base, tmplPath); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filepath.Base(tmplPath)
}

// partialDirsFor は部品テンプレの探索順（ON バンドル → 消費側。同名は消費側が後勝ち）。
// 各側ではオーバーレイ層 → 直下の順に見る（直下が最優先）。
func partialDirsFor(c Target, on []bundle.Bundle) []string {
	var dirs []string
	add := func(base string) {
		for _, ov := range targetLayers(base) {
			if p := filepath.Join(base, ov, bundle.PartialsDirName); isDir(p) {
				dirs = append(dirs, p)
			}
		}
		if p := filepath.Join(base, bundle.PartialsDirName); isDir(p) {
			dirs = append(dirs, p)
		}
	}
	for _, b := range on {
		add(b.Dir)
	}
	add(c.Dir)
	return dirs
}

// targetTemplates はターゲットのテンプレ一覧を**ターゲット相対パス**で返す（ソート済み）。
//
// 見るのは「ターゲット直下」と「OverlayDirs の各ディレクトリ直下」の 2 段だけ。
// 一般のサブディレクトリを再帰してはいけない —— 配下にプロジェクトを並べる設定リポジトリで、
// 親ターゲットが子の *.tmpl を自分のものとして拾ってしまう。
func targetTemplates(dir string) ([]string, error) {
	var out []string
	for _, layer := range append([]string{"."}, targetLayers(dir)...) {
		hits, err := filepath.Glob(filepath.Join(dir, layer, "*.tmpl"))
		if err != nil {
			return nil, fmt.Errorf(msg.M.Apply.TmplScanFailed, err)
		}
		for _, h := range hits {
			rel, err := filepath.Rel(dir, h)
			if err != nil {
				return nil, err
			}
			out = append(out, rel)
		}
	}
	slices.Sort(out)
	return out, nil
}

// targetLayers はターゲットが持つオーバーレイ層（ドット始まりの 1 階層目ディレクトリ）。
//
// 棚に依存せず自分で数えるので、Root を解決する前のターゲット探索からも使える。
// .git は層にしない（中を舐める意味が無い）。
func targetLayers(dir string) []string {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		name := e.Name()
		if name == ".git" || !strings.HasPrefix(name, ".") || !isDir(filepath.Join(dir, name)) {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// flatten はターゲット相対パスを退避ラベル用の 1 語へ潰す（.claude/settings.json → .claude-settings.json）。
// 退避先は全ターゲット共通なので、階層をラベルに畳まないと別ターゲットの同名生成物と衝突する。
func flatten(rel string) string {
	return strings.ReplaceAll(rel, string(filepath.Separator), "-")
}

// HomeOrder はバンドルルートの解決順（表示用の唯一の正）。
// 以前は README・cobra の Long・help・エラー文の 4〜5 箇所に別々の文字列で散っていた。
func HomeOrder() string {
	return fmt.Sprintf(msg.M.Apply.HomeOrder, EnvHome, TargetConfName, flags.KeyBundleRoot, BundleDirName)
}

// ConfHome は conf 由来のバンドルルート指定。ゼロ値は「どの conf も指定していない」。
type ConfHome struct {
	Dir string // 解決済みの絶対パス
	Src string // "<conf のパス>:<行>"（エラーと表示に使う）
}

// ScanConfHome は全ターゲットの llmtpl.conf を読み、bundle_root の指定を 1 つに畳む。
//
// **全ターゲットを見る**のが要点。最初の引数だけを見る案は、リポジトリルートがターゲットで
// ないとき（ai-settings がそう）に必ず空振りする。先頭ターゲットだけを見る案は、パス順で
// たまたま先頭に来た 1 件が他の全ターゲットのルートを黙って決めてしまう。
// 食い違いは解決不能なのでエラーにする（どちらかを勝たせると、負けた側は「書いたのに効かない」）。
func ScanConfHome(targets []Target) (ConfHome, error) {
	var out ConfHome
	for _, t := range targets {
		path := filepath.Join(t.Dir, TargetConfName)
		conf, err := flags.ParseConf(path)
		if err != nil {
			return ConfHome{}, err
		}
		if conf.BundleRoot == "" {
			continue
		}
		cur := ConfHome{Dir: conf.BundleRoot, Src: fmt.Sprintf("%s:%d", path, conf.BundleRootLine)}
		if out.Dir != "" && out.Dir != cur.Dir {
			return ConfHome{}, fmt.Errorf(msg.M.Apply.ConfHomeConflict,
				flags.KeyBundleRoot, out.Src, out.Dir, cur.Src, cur.Dir)
		}
		out = cur
	}
	return out, nil
}

// HomeSource は「どの段でバンドルルートが決まったか」。表示に使う。
// 値の一致から出典を推測すると、--tpl-home と conf がたまたま同値のときに嘘をつく。
type HomeSource string

const (
	HomeFromFlag   HomeSource = "flag"
	HomeFromEnv    HomeSource = "env"
	HomeFromConf   HomeSource = "conf"
	HomeFromWalkUp HomeSource = "walkup"
	HomeFromXDG    HomeSource = "xdg"
)

// Label は表示用の文言を返す。識別子と表示を分けてあるので、== による比較は
// ロケールに左右されない（分けないと、言語を切り替えた瞬間に比較が全部外れる）。
func (h HomeSource) Label() string {
	switch h {
	case HomeFromEnv:
		return fmt.Sprintf(msg.M.Apply.HomeLabelEnv, EnvHome)
	case HomeFromConf:
		return fmt.Sprintf(msg.M.Apply.HomeLabelConf, TargetConfName, flags.KeyBundleRoot)
	case HomeFromWalkUp:
		return msg.M.Apply.HomeLabelWalkUp
	case HomeFromFlag:
		return "--tpl-home"
	case HomeFromXDG:
		return "XDG"
	}
	return string(h)
}

// FindTplHome はバンドルルートを解決する。優先順は HomeOrder。
//
// explicit / env / conf はいずれも「人が明示的に書いた」指定なので、指す先が無ければ
// **次の候補へ落ちずにエラー**にする。落として通すと、タイポが「別のルートで生成が成功する」に化ける。
//
// 返すのは常に**絶対パス**。Root.Dir が相対だと、配下判定（Under）が Rel のエラーで
// 判定不能になり、ガードが素通りする。
func FindTplHome(explicit string, conf ConfHome, start string) (string, HomeSource, error) {
	if explicit != "" {
		if !isDir(explicit) {
			return "", "", fmt.Errorf(msg.M.Apply.ExplicitHomeMissing, explicit)
		}
		abs, err := filepath.Abs(explicit)
		return abs, HomeFromFlag, err
	}
	if env := os.Getenv(EnvHome); env != "" {
		if !isDir(env) {
			return "", "", fmt.Errorf(msg.M.Apply.EnvHomeMissing, EnvHome, env)
		}
		abs, err := filepath.Abs(env)
		return abs, HomeFromEnv, err
	}
	if conf.Dir != "" {
		// conf 由来は resolveConfPath が既に絶対化している
		if !isDir(conf.Dir) {
			return "", "", fmt.Errorf(msg.M.Apply.ConfHomeMissing, conf.Src, flags.KeyBundleRoot, conf.Dir)
		}
		return conf.Dir, HomeFromConf, nil
	}
	if found, ok := walkUpForBundleRoot(start); ok {
		return found, HomeFromWalkUp, nil
	}
	if xdg := xdgHome(); xdg != "" && isDir(xdg) {
		return xdg, HomeFromXDG, nil
	}
	return "", "", fmt.Errorf(msg.M.Apply.HomeNotFound, HomeOrder())
}

// walkUpForBundleRoot は start から親へ辿って <dir>/llm-tpl を探す。
func walkUpForBundleRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if cand := filepath.Join(dir, BundleDirName); isDir(cand) {
			return cand, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func xdgHome() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "llmtpl")
}

// skipDirs は中身も自身もターゲット判定から外すディレクトリ名。
// いずれも「*.tmpl を持つがターゲットではない」ものを弾くためにある:
// llm-tpl/ はバンドル断片、partials/ はテンプレの部品置き場、.archive/ は退避物（生成物の写し）、
// testdata/ と examples/ は「動かして見せるための入力一式」（実際に適用する対象ではない）。
//
// testdata は Go が予約している名前なので誤爆しない。examples は一般的な名前だが、
// サンプルの中の .claude を本番設定として書き換えるほうが害が大きいので同様に外す
// （本当に対象にしたければディレクトリを明示指定すればよい）。
var skipDirs = map[string]bool{
	BundleDirName:          true,
	bundle.PartialsDirName: true,
	archiveDirName:         true,
	"node_modules":         true,
	"vendor":               true,
	"testdata":             true,
	"examples":             true,
}

// DiscoverTargets は start 自身と**その配下**からターゲットを探す。
// ターゲットの条件: **そのディレクトリ自身**に llmtpl.conf か *.tmpl が 1 つ以上ある。
// ".claude" という名前は前提にしないので、<repo>/.claude も「それ自体が設定ディレクトリ」
// （~/.claude のミラー等）も同じ規約で拾える。
//
// **常に配下も探す**（旧 -r 相当を既定にした）。設定リポジトリの中では再帰が唯一の正解で、
// 「自身だけ」を既定にすると repo root からの apply が必ず失敗し、-r が「常に付ける儀式」に
// なっていた。自身がターゲットでも探索は止めない（1 リポジトリに複数あってよい。
// 例: <repo>/CLAUDE.local.md.tmpl と <repo>/.claude/ が別ターゲットになる構成）。
//
// 暴走は 3 段で止める: skipDirs / ドット始まりへは降りない / **別リポジトリへは降りない**。
func DiscoverTargets(start string) ([]Target, error) {
	var out []Target
	err := filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := filepath.Base(path)
		if path != start && skipDirs[name] {
			return filepath.SkipDir
		}
		// ドット始まり（.claude / .git 等）は**ターゲットにしないし中へも降りない**。
		// .claude はターゲット（プロジェクトルート）のオーバーレイ層であって、それ自体は
		// 独立したターゲットではない。ここで弾くことで <P> と <P>/.claude の二重ターゲットが
		// 構造的に起こらなくなり、フラグを親から継承する機構（＝ 暗黙の継承）が不要になる。
		if path != start && strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		// 別リポジトリは**そのルート自身もターゲットにしない**。$HOME のような広い場所を
		// 指したときに、無関係なリポジトリまで設定してしまうのを防ぐ（start 自身は常に対象）。
		// ターゲットがプロジェクトルートになった今、判定より前に弾かないと別リポジトリの
		// ルートが拾われる。worktree / submodule では .git がファイルなのでファイルも見る。
		if path != start {
			if g := filepath.Join(path, ".git"); isDir(g) || isFile(g) {
				return filepath.SkipDir
			}
		}
		if isTarget(path) {
			out = append(out, Target{Dir: path})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf(msg.M.Apply.TargetScanFailed, err)
	}
	return out, nil
}

// isTarget は「そのディレクトリが llmtpl.conf を持つか」。**これがターゲットの唯一の条件**。
//
// 以前は「*.tmpl を持つこと」も条件だったが外した。受け口（*.tmpl）はターゲットの中身であって
// ターゲットの目印ではない。両方を条件にすると <P> と <P>/.claude が同時にターゲット化し
// （前者は .claude/*.tmpl を、後者は自分の *.tmpl を数えるため）、conf を持たない後者が
// 全フラグ OFF で生成物を薄い内容に上書きする。目印を 1 つに絞れば構造的に起きない。
func isTarget(dir string) bool {
	if isFile(filepath.Join(dir, TargetConfName)) {
		return true
	}
	// ⚠️ 移行ガード（一時的）: conf がオーバーレイ層に残っている旧レイアウトも拾う。
	// 拾わないと「conf を書いたのに何も起きない」で終わるので、拾ったうえで Apply が
	// 移し先を名指しして落とす。全環境の移行が終わったら消してよい。
	for _, ov := range targetLayers(dir) {
		if isFile(filepath.Join(dir, ov, TargetConfName)) {
			return true
		}
	}
	return false
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
