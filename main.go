// llmtpl は LLM 設定（CLAUDE.md / .claude 配下）をフラグ単位のバンドルから合成する CLI。
//
// このファイルは表示と終了コードだけを担う（何が差分かの判断は apply.Report.Diffs）。
// 終了コード: 0 正常 / 1 エラー / 2 check で差分あり / 130 対話モードを中止した。
//
// CLI は cobra（姉妹ツール dq と同じ）。位置引数とオプションの混在・サブコマンド別の help・
// シェル補完（llmtpl completion zsh）は cobra 側の機能に任せる。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ryokwkm/llmtpl/internal/apply"
	"github.com/ryokwkm/llmtpl/internal/bundle"
	"github.com/ryokwkm/llmtpl/internal/flags"
	"github.com/ryokwkm/llmtpl/internal/link"
	"github.com/ryokwkm/llmtpl/internal/msg"
)

// version は -ldflags "-X main.version=..." で埋める。
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "llmtpl: %v\n", err)
		os.Exit(exitCode(err))
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "llmtpl",
		Short:         msg.M.Cmd.Short,
		Long:          fmt.Sprintf(msg.M.Cmd.Long, apply.HomeOrder()),
		Version:       version,
		SilenceUsage:  true, // 実行時エラーで usage を撒かない（main が 1 行で出す）
		SilenceErrors: true,
		// 引数なしで叩かれたときだけ対話モードへ入る。cobra は Run を持つ root でも
		// 未知のサブコマンド名はエラーにするので（legacyArgs）、`llmtpl typo` の挙動は変わらない
		RunE: func(cmd *cobra.Command, _ []string) error { return runInteractive(cmd) },
	}
	root.AddCommand(
		newApplyCmd(false),
		newApplyCmd(true),
		newStatusCmd(),
		newBundlesCmd(),
	)
	return root
}

// diffErr は check で差分があったことを表す（exit 2 用）。
type diffErr struct{ n int }

func (e diffErr) Error() string {
	return fmt.Sprintf(msg.M.Cmd.StaleGenerated, e.n)
}

func exitCode(err error) int {
	switch err.(type) {
	case diffErr:
		return 2
	case canceledErr:
		return 130 // SIGINT の慣習に合わせる（人が中止したのであってエラーではない）
	}
	return 1
}

// commonFlags は複数サブコマンドで共通のオプション。
type commonFlags struct {
	tplHome string
	dryRun  bool
	verbose bool
}

func (c *commonFlags) register(cmd *cobra.Command, withDryRun bool) {
	f := cmd.Flags()
	f.StringVar(&c.tplHome, "tpl-home", "", msg.M.Cmd.FlagTplHome)
	if withDryRun {
		f.BoolVar(&c.dryRun, "dry-run", false, msg.M.Cmd.FlagDryRun)
	}
}

func newApplyCmd(checkOnly bool) *cobra.Command {
	c := &commonFlags{}
	cmd := &cobra.Command{
		Use:   "apply [dir...]",
		Short: msg.M.Cmd.ApplyShort,
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return runApply(args, c, checkOnly)
		},
	}
	if checkOnly {
		cmd.Use = "check [dir...]"
		cmd.Short = msg.M.Cmd.CheckShort
	}
	c.register(cmd, !checkOnly)
	cmd.Flags().BoolVarP(&c.verbose, "verbose", "v", false, msg.M.Cmd.FlagVerbose)
	return cmd
}

func runApply(dirs []string, c *commonFlags, checkOnly bool) error {
	if checkOnly {
		c.dryRun = true
	}
	targets, root, err := resolveScope(dirs, c)
	if err != nil {
		return err
	}

	o := apply.Options{DryRun: c.dryRun}
	diffs := 0
	for _, tg := range targets {
		rep, err := root.Apply(tg, o)
		if err != nil {
			return err // 1 つでも失敗したら以降へ進まない（半端な状態を作らない）
		}
		diffs += rep.Diffs()
		printReport(tg, rep, o.DryRun, c.verbose)
	}
	if checkOnly && diffs > 0 {
		return diffErr{n: diffs}
	}
	return nil
}

func newStatusCmd() *cobra.Command {
	c := &commonFlags{}
	cmd := &cobra.Command{
		Use:   "status [dir...]",
		Short: msg.M.Cmd.StatusShort,
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			targets, root, err := resolveScope(args, c)
			if err != nil {
				return err
			}
			return printStatus(targets, root)
		},
	}
	c.register(cmd, false)
	return cmd
}

// newBundlesCmd はバンドルのカタログを出す。旧名 flags を alias として受理する
// （CLI では "flags" が「オプション一覧」と誤読されるので改名した。実体はバンドルの一覧で、
// 「フラグ」は ON/OFF という状態を指す語に寄せた）。
func newBundlesCmd() *cobra.Command {
	var tplHome string
	cmd := &cobra.Command{
		Use:     "bundles",
		Aliases: []string{"flags"},
		Short:   msg.M.Cmd.BundlesShort,
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			// apply と同じ経路を通す。ここが別経路だと、bundles が apply とは違うルートの
			// カタログを出す —— カタログは「conf に書いてよいフラグ名」の一次情報（Validate の
			// 母集合そのもの）なので、ずれると「表示にあった名前を書いたのに未知のフラグで落ちる」
			_, root, src, err := resolveRoot(nil, tplHome)
			if err != nil {
				return err
			}
			return printBundles(root, src) // ターゲット 0 件でもカタログは出す
		},
	}
	cmd.Flags().StringVar(&tplHome, "tpl-home", "", msg.M.Cmd.FlagTplHome)
	return cmd
}

// absDirs は対象ディレクトリ列を絶対パスにする（省略時は cwd）。
func absDirs(dirs []string) ([]string, error) {
	if len(dirs) == 0 {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		return []string{wd}, nil
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, err
		}
		out = append(out, abs)
	}
	return out, nil
}

// resolveRoot は「ターゲット群 + バンドルルート」を 1 本の経路で揃える。全サブコマンドがこれを通る。
//
// 順序は固定: absDirs → DiscoverTargets → ソート → ScanConfHome → FindTplHome → LoadRoot。
// conf 由来のルート指定を読むにはターゲットが先に要るので、この順以外に組めない。
// ルートの読み込みはターゲット数に関係なく 1 回（Root はプロセスに 1 つ）。
func resolveRoot(dirs []string, tplHome string) ([]apply.Target, apply.Root, string, error) {
	abs, err := absDirs(dirs)
	if err != nil {
		return nil, apply.Root{}, "", err
	}

	var targets []apply.Target
	seen := map[string]bool{}
	for _, d := range abs {
		found, err := apply.DiscoverTargets(d)
		if err != nil {
			return nil, apply.Root{}, "", err
		}
		for _, tg := range found {
			if !seen[tg.Dir] {
				seen[tg.Dir] = true
				targets = append(targets, tg)
			}
		}
	}
	slices.SortFunc(targets, func(a, b apply.Target) int { return strings.Compare(a.Dir, b.Dir) })

	confHome, err := apply.ScanConfHome(targets)
	if err != nil {
		return nil, apply.Root{}, "", err
	}
	home, src, err := apply.FindTplHome(tplHome, confHome, abs[0])
	if err != nil {
		return nil, apply.Root{}, "", err
	}
	root, err := apply.LoadRoot(home)
	if err != nil {
		return nil, apply.Root{}, "", err
	}

	// ルートが探索ツリーの内側を非標準名で指していると、その中のバンドルが「*.tmpl を持つ
	// ディレクトリ」としてターゲットに拾われ、apply 全体が落ちる（skipDirs は文字列 llm-tpl
	// しか見ない）。引数で名指しされたものは意図的な指定とみなして残す
	kept := targets[:0]
	dropped := 0
	for _, tg := range targets {
		if !slices.Contains(abs, tg.Dir) && apply.Under(root.Dir, tg.Dir) {
			dropped++
			continue
		}
		kept = append(kept, tg)
	}
	targets = kept
	if dropped > 0 {
		fmt.Printf(msg.M.Cmd.DroppedTargets, dropped)
	}

	label := src.Label()
	if src == apply.HomeFromConf {
		// 出典は「llmtpl.conf の bundle_root」ではなく、実際に書いてある conf のパスまで出す
		label = fmt.Sprintf(msg.M.Apply.HomeLabelConf, confHome.Src, flags.KeyBundleRoot)
	}
	return targets, root, label, nil
}

// resolveScope は resolveRoot にターゲット 0 件の判定と件数表示を足したもの（apply / check / status 用）。
func resolveScope(dirs []string, c *commonFlags) ([]apply.Target, apply.Root, error) {
	targets, root, src, err := resolveRoot(dirs, c.tplHome)
	if err != nil {
		return nil, apply.Root{}, err
	}
	if len(targets) == 0 {
		abs, _ := absDirs(dirs)
		return nil, apply.Root{}, fmt.Errorf(msg.M.Cmd.NoTargets, strings.Join(abs, ", "), apply.TargetConfName)
	}
	// 探索が既定で配下まで及ぶので、何件を対象にしたかを必ず見せる（誤爆の可視化）
	if len(targets) > 1 {
		fmt.Printf(msg.M.Cmd.TargetCount, len(targets))
	}
	// 解決したルートは常に見せる。conf 由来だと「離れた 1 ファイルが全体を変える」ので、
	// 出典まで出さないと事故が静かになる
	fmt.Printf(msg.M.Cmd.BundleRootLine, root.Dir, src)
	return targets, root, nil
}

// printReport は 1 ターゲット分の結果を表示する（判断はしない）。
func printReport(tg apply.Target, rep apply.Report, dryRun, verbose bool) {
	on := msg.M.Cmd.OnNone
	if len(rep.On) > 0 {
		on = strings.Join(rep.On, ", ")
	}
	fmt.Printf("▸ %s  [ON: %s]\n", rel(tg.Dir), on)

	for _, t := range rep.Targets {
		switch {
		case t.Skipped != "":
			fmt.Printf(msg.M.Cmd.Skip, rel(t.Dest), t.Skipped)
		case t.Changed && dryRun:
			fmt.Printf(msg.M.Cmd.Diff, rel(t.Dest))
		case t.Changed:
			fmt.Printf(msg.M.Cmd.Generated, rel(t.Dest))
		default:
			fmt.Printf(msg.M.Cmd.Unchanged, rel(t.Dest))
		}
		if t.Archived != "" {
			fmt.Printf(msg.M.Cmd.ArchivedEntity, t.Archived)
			fmt.Print(msg.M.Cmd.ArchivedHint)
		}
		if t.WouldArchive {
			fmt.Print(msg.M.Cmd.WouldArchive)
		}
	}

	// 受け皿が無い断片は **既定で見せる**。apply では解消しないので差分には数えないが、
	// 黙っていると「実体は配られたのに指示だけ届かない」状態に誰も気づけない
	for _, o := range rep.Orphans {
		fmt.Printf(msg.M.Cmd.Orphan,
			o.Bundle, o.File, o.File)
	}

	for _, l := range rep.Links {
		switch l.Kind {
		case link.KindCreated:
			if dryRun {
				fmt.Printf(msg.M.Cmd.LinkPlanned, rel(l.Path), l.Target)
			} else {
				fmt.Printf(msg.M.Cmd.Linked, rel(l.Path), l.Target)
			}
		case link.KindRemoved:
			if dryRun {
				fmt.Printf(msg.M.Cmd.UnlinkPlanned, rel(l.Path))
			} else {
				fmt.Printf(msg.M.Cmd.Unlinked, rel(l.Path))
			}
		case link.KindArchived:
			fmt.Printf(msg.M.Cmd.LinkArchived, l.Note)
		case link.KindConflict:
			// apply では解消しない設定ミスなので、差分に数えない代わりに常に見せる
			fmt.Printf(msg.M.Cmd.LinkConflict, l.Note)
		case link.KindKept:
			if verbose {
				fmt.Printf(msg.M.Cmd.LinkKept, rel(l.Path))
			}
		}
	}
}

func printStatus(targets []apply.Target, root apply.Root) error {
	names := root.Names() // バンドルルートの行は resolveScope が出す
	if len(names) == 0 {
		fmt.Println(msg.M.Cmd.NoBundles)
		return nil
	}

	rows := [][]string{append([]string{msg.M.Cmd.ColTarget}, names...)}
	for _, tg := range targets {
		eff, err := root.Flags(tg)
		if err != nil {
			return err
		}
		row := make([]string, 0, len(names)+1)
		row = append(row, rel(tg.Dir))
		for _, n := range names {
			if eff[n] {
				row = append(row, "ON")
			} else {
				row = append(row, "-")
			}
		}
		rows = append(rows, row)
	}
	printTable(rows)
	return nil
}

func printBundles(root apply.Root, src string) error {
	fmt.Printf(msg.M.Cmd.BundleRootLine, root.Dir, src)
	rows := [][]string{{msg.M.Cmd.ColBundle, msg.M.Cmd.ColContents, msg.M.Cmd.ColDesc}}
	for _, b := range root.Bundles {
		meta, err := bundle.LoadMeta(b)
		if err != nil {
			return err
		}
		contents, err := bundle.Contents(b)
		if err != nil {
			return err
		}
		rows = append(rows, []string{b.Name, strings.Join(contents, " "), meta.Description})
	}
	printTable(rows)
	return nil
}

// printTable は表示幅で桁を揃えて表を出す（最終列はパディングしない）。
//
// **text/tabwriter を使わない理由**: あちらはセル幅をルーン数で数えて表示幅を見ないので、
// 日本語の列見出しを渡すと見出しだけが右へずれる（「ターゲット」は 5 と数えられるが
// 端末では 10 桁を占める）。中核概念を伝える表が壊れて見えるのは避けたい。
func printTable(rows [][]string) {
	var width []int
	for _, r := range rows {
		for i, c := range r {
			for len(width) <= i {
				width = append(width, 0)
			}
			if w := displayWidth(c); w > width[i] {
				width[i] = w
			}
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", width[i]-displayWidth(c)+2))
			}
		}
		fmt.Println(b.String())
	}
}

// displayWidth は端末での表示幅（East Asian Wide / Fullwidth と絵文字を 2 と数える）。
// 完全な Unicode 実装ではなく、この CLI が出す文字（日本語・記号・絵文字）を正しく扱える範囲に絞る。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w++
		if isWideRune(r) {
			w++
		}
	}
	return w
}

func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // ハングル字母
		r >= 0x2E80 && r <= 0x303E,   // CJK 部首・康熙部首・CJK 記号
		r >= 0x3041 && r <= 0x33FF,   // ひらがな・カタカナ・ハングル・CJK 互換
		r >= 0x3400 && r <= 0x4DBF,   // CJK 拡張 A
		r >= 0x4E00 && r <= 0x9FFF,   // CJK 統合漢字
		r >= 0xA000 && r <= 0xA4CF,   // イ文字
		r >= 0xAC00 && r <= 0xD7A3,   // ハングル音節
		r >= 0xF900 && r <= 0xFAFF,   // CJK 互換漢字
		r >= 0xFE30 && r <= 0xFE6F,   // CJK 互換形・小字形
		r >= 0xFF00 && r <= 0xFF60,   // 全角形
		r >= 0xFFE0 && r <= 0xFFE6,   // 全角記号
		r >= 0x1F300 && r <= 0x1FAFF: // 絵文字
		return true
	}
	return false
}

// rel は cwd 配下のパスを相対表示にする（**表示のためだけ**。生成物の内容には使わない）。
//
// cwd そのものは "." ではなくディレクトリ名で出す。ターゲット（= プロジェクトルート）の中で
// apply を叩くのが標準の使い方なので、"▸ ." では何を処理したのか読み取れない。
func rel(p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	r, err := filepath.Rel(wd, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return p
	}
	if r == "." {
		return filepath.Base(p)
	}
	return r
}
