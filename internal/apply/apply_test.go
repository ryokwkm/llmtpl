package apply

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryokwkm/llmtpl/internal/bundle"
	"github.com/ryokwkm/llmtpl/internal/state"

	"github.com/ryokwkm/llmtpl/internal/msg"
)

// テストは実運用と同じ <repo>/.claude 構造で行う（ターゲットの規約は
// 「llmtpl.conf か *.tmpl を持つディレクトリ」で、この名前に依存しない）。
// 定数は本体側（apply.go の bundle.DefaultOverlay）を使う。

// fixture は バンドルルート + ターゲット 1 つ を持つ一時環境を作る。
type fixture struct {
	base     string // 設定リポジトリ相当のルート
	tplHome  string // <root>/llm-tpl
	projRoot string // <root>/proj（ターゲット = プロジェクトルート。Apply に渡すのはこちら）
	target   string // <root>/proj/.claude（オーバーレイ層。受け口と生成物が落ちる場所）
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{
		base:     root,
		tplHome:  filepath.Join(root, BundleDirName),
		projRoot: filepath.Join(root, "proj"),
		target:   filepath.Join(root, "proj", bundle.DefaultOverlay),
	}
	mkdirAll(t, f.tplHome)
	mkdirAll(t, f.target)
	return f
}

func mkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bundleFile はバンドル <name> のオーバーレイ層に置く。
// テストが渡す rel は "rules/x.md" のようにターゲット内の位置で書き、ここで .claude/ を被せる
// （バンドルの中身はターゲットの中へそのまま重なるため）。
func (f fixture) bundleFile(t *testing.T, name, rel, content string) {
	t.Helper()
	writeFile(t, filepath.Join(f.tplHome, name, bundle.DefaultOverlay, rel), content)
}

// bundleRootFile はバンドルのルート直下（オーバーレイ層の外）に置く = <T> 直下へ届く断片。
func (f fixture) bundleRootFile(t *testing.T, name, rel, content string) {
	t.Helper()
	writeFile(t, filepath.Join(f.tplHome, name, rel), content)
}

// confFile はターゲットのルート直下に llmtpl.conf を置く（conf はオーバーレイ層ではなくルートに住む）。
func (f fixture) confFile(t *testing.T, content string) {
	t.Helper()
	writeFile(t, filepath.Join(f.projRoot, TargetConfName), content)
}

// targetFile はターゲットにファイルを置く
func (f fixture) targetFile(t *testing.T, rel, content string) {
	t.Helper()
	writeFile(t, filepath.Join(f.target, rel), content)
}

func (f fixture) root(t *testing.T) Root {
	t.Helper()
	r, err := LoadRoot(f.tplHome)
	if err != nil {
		t.Fatalf("LoadRoot が失敗: %v", err)
	}
	return r
}

// applyErr は Apply（必要なら LoadRoot）のエラーを返す（異常系テスト用）
func (f fixture) applyErr(t *testing.T) error {
	t.Helper()
	r, err := LoadRoot(f.tplHome)
	if err != nil {
		return err
	}
	_, err = r.Apply(Target{Dir: f.projRoot}, Options{})
	return err
}

func (f fixture) run(t *testing.T, o Options) Report {
	t.Helper()
	rep, err := f.root(t).Apply(Target{Dir: f.projRoot}, o)
	if err != nil {
		t.Fatalf("Apply が失敗: %v", err)
	}
	return rep
}

func (f fixture) generated(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.target, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- TplHome の解決 -------------------------------------------------------

func TestFindTplHome_明示指定が最優先(t *testing.T) {
	f := newFixture(t)
	t.Setenv("LLMTPL_HOME", filepath.Join(t.TempDir(), "env-home"))

	got, _, err := FindTplHome(f.tplHome, ConfHome{}, f.target)
	if err != nil {
		t.Fatal(err)
	}
	if got != f.tplHome {
		t.Errorf("明示指定が使われていない: %q", got)
	}
}

// conf 由来の指定は env より下・親探索より上（HomeOrder のとおり）。
func TestFindTplHome_conf由来は環境変数より下(t *testing.T) {
	f := newFixture(t)
	envHome := filepath.Join(t.TempDir(), "env-home")
	mkdirAll(t, envHome)
	t.Setenv("LLMTPL_HOME", envHome)

	got, _, err := FindTplHome("", ConfHome{Dir: f.tplHome, Src: "x:1"}, f.target)
	if err != nil {
		t.Fatal(err)
	}
	if got != envHome {
		t.Errorf("環境変数が conf より優先されていない: %q", got)
	}
}

func TestFindTplHome_conf由来は親探索より上(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(t.TempDir(), "elsewhere")
	mkdirAll(t, other)

	// f.target から親を辿れば f.tplHome が見つかる状況で、conf が別の場所を指す
	got, src, err := FindTplHome("", ConfHome{Dir: other, Src: "x:1"}, f.target)
	if err != nil {
		t.Fatal(err)
	}
	if got != other {
		t.Errorf("conf 由来の指定が親探索に負けている: %q", got)
	}
	if src != HomeFromConf {
		t.Errorf("出典が conf になっていない: %q", src)
	}
}

// 人が明示的に書いた指定は、指す先が無ければ次の候補へ落とさずエラーにする
// （落とすとタイポが「別のルートで生成が成功する」に化ける）。
func TestFindTplHome_conf由来が存在しなければ親探索へ落ちずエラー(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(t.TempDir(), "no-such")

	_, _, err := FindTplHome("", ConfHome{Dir: missing, Src: "proj/.claude/llmtpl.conf:3"}, f.target)
	if err == nil {
		t.Fatal("存在しない conf 指定がエラーになりません（親探索へ落ちた疑い）")
	}
	if !strings.Contains(err.Error(), "proj/.claude/llmtpl.conf:3") {
		t.Errorf("エラーに出典が入っていない: %v", err)
	}
}

func TestConfHomeOf(t *testing.T) {
	t.Run("conf が指定していなければ意見なし", func(t *testing.T) {
		d := t.TempDir()
		writeFile(t, filepath.Join(d, TargetConfName), "wiki = true\n")
		got, err := ConfHomeOf(d)
		if err != nil {
			t.Fatal(err)
		}
		if got.Dir != "" {
			t.Errorf("意見なしを期待: %+v", got)
		}
	})

	t.Run("conf が無くても意見なし（エラーにしない）", func(t *testing.T) {
		got, err := ConfHomeOf(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if got.Dir != "" {
			t.Errorf("意見なしを期待: %+v", got)
		}
	})

	t.Run("指定していればそれを採り、出典を持つ", func(t *testing.T) {
		d := t.TempDir()
		writeFile(t, filepath.Join(d, TargetConfName), "bundle_root = /opt/b\n")
		got, err := ConfHomeOf(d)
		if err != nil {
			t.Fatal(err)
		}
		if got.Dir != "/opt/b" {
			t.Errorf("採用されていない: %+v", got)
		}
		if !strings.Contains(got.Src, TargetConfName) || !strings.HasSuffix(got.Src, ":1") {
			t.Errorf("出典が不正: %q", got.Src)
		}
	})
}

// defaults.conf はバンドルルートの中にあるので、そこでルートを指定するのは定義上遅すぎる。
func TestLoadRoot_defaultsConfのbundle_rootはエラー(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.tplHome, DefaultsConfName), "bundle_root = /opt/b\n")
	_, err := LoadRoot(f.tplHome)
	if err == nil {
		t.Fatal("defaults.conf の bundle_root がエラーになりません（黙って無視されると書いた本人が気づけない）")
	}
	if !strings.Contains(err.Error(), DefaultsConfName) {
		t.Errorf("どのファイルかがメッセージに無い: %v", err)
	}
}

// 予約キーと同名のバンドルは、conf で ON にできないので静かに死ぬ。作らせない。
func TestLoadRoot_予約キーと同名のバンドルはエラー(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "bundle_root", "CLAUDE.md.tmpl", "x\n")
	_, err := LoadRoot(f.tplHome)
	if err == nil {
		t.Fatal("予約キーと同名のバンドルがエラーになりません")
	}
	if !strings.Contains(err.Error(), msg.Lit(msg.M.Apply.ReservedBundleName)) {
		t.Errorf("理由がメッセージに無い: %v", err)
	}
}

// root.Dir が相対だと Under が Rel のエラーで判定不能になり、ガードが素通りする。
func TestFindTplHome_明示指定と環境変数は絶対パスへ正規化される(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.base)

	got, _, err := FindTplHome(BundleDirName, ConfHome{}, f.target) // 相対で渡す
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("--tpl-home が相対のまま返っている: %q", got)
	}

	t.Setenv("LLMTPL_HOME", BundleDirName)
	got, _, err = FindTplHome("", ConfHome{}, f.target)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("$LLMTPL_HOME が相対のまま返っている: %q", got)
	}
}

func TestUnder(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		base, path string
		want       bool
		why        string
	}{
		{"/r", "/r/a", true, "直下"},
		{"/r", "/r/a/b", true, "孫"},
		{"/r", "/r", false, "自身は配下ではない"},
		{"/r", "/other", false, "兄弟"},
		{"/r", "/", false, "親"},
		// 「.. で始まる文字列」で判定すると、.. で始まる名前に誤って一致する
		{"/r", "/r" + sep + "..foo", true, "..foo は配下（名前の一部としての ..）"},
		{"/r", "/r" + sep + "...", true, "... も配下"},
		// 判定不能は安全側（配下とみなす）へ倒す。呼び出し側はどちらも「配下なら弾く」ガード
		{"rel", "/abs", true, "相対と絶対の組は判定不能 → 安全側"},
	}
	for _, c := range cases {
		if got := Under(c.base, c.path); got != c.want {
			t.Errorf("Under(%q, %q) = %v, want %v（%s）", c.base, c.path, got, c.want, c.why)
		}
	}
}

func TestFindTplHome_明示指定が存在しなければエラー(t *testing.T) {
	if _, _, err := FindTplHome(filepath.Join(t.TempDir(), "no-such"), ConfHome{}, t.TempDir()); err == nil {
		t.Error("存在しない指定がエラーになりません")
	}
}

func TestFindTplHome_環境変数(t *testing.T) {
	f := newFixture(t)
	other := t.TempDir() // walk-up で見つからない場所から探させる
	t.Setenv("LLMTPL_HOME", f.tplHome)

	got, _, err := FindTplHome("", ConfHome{}, other)
	if err != nil {
		t.Fatal(err)
	}
	if got != f.tplHome {
		t.Errorf("環境変数が使われていない: %q", got)
	}
}

// ターゲットから親を辿って llm-tpl/ を見つける（設定リポジトリの中では指定なしで解決する経路）
func TestFindTplHome_親を辿って発見(t *testing.T) {
	f := newFixture(t)
	t.Setenv("LLMTPL_HOME", "")
	deep := filepath.Join(f.target, "a/b/c")
	mkdirAll(t, deep)

	got, _, err := FindTplHome("", ConfHome{}, deep)
	if err != nil {
		t.Fatal(err)
	}
	if got != f.tplHome {
		t.Errorf("walk-up で見つけられていない: %q", got)
	}
}

func TestFindTplHome_見つからなければ案内付きエラー(t *testing.T) {
	t.Setenv("LLMTPL_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "empty-xdg"))

	_, _, err := FindTplHome("", ConfHome{}, t.TempDir())
	if err == nil {
		t.Fatal("エラーを期待したが nil")
	}
	if !strings.Contains(err.Error(), BundleDirName) || !strings.Contains(err.Error(), "LLMTPL_HOME") {
		t.Errorf("解決方法の案内がメッセージに無い: %v", err)
	}
}

// ---- ターゲットの探索 ----------------------------------------------

// **ターゲットの条件は「そのディレクトリが llmtpl.conf を持つ」の 1 つだけ。**
// 受け口（*.tmpl）はターゲットの中身であって目印ではない —— 目印にすると
// <P> と <P>/.claude が同時にターゲット化し、conf を持たない後者が全 OFF で生成物を潰す。
func TestDiscoverTargets_confを持つディレクトリだけを拾う(t *testing.T) {
	root := t.TempDir()
	// 対象: conf を持つ
	writeFile(t, filepath.Join(root, "a", TargetConfName), "")
	// 対象: 旧レイアウト（conf が層に残っている）。移行ガードのために拾う
	writeFile(t, filepath.Join(root, "b", bundle.DefaultOverlay, TargetConfName), "")
	// 非対象: 受け口だけ（conf が無い）
	writeFile(t, filepath.Join(root, "c", bundle.DefaultOverlay, "CLAUDE.md.tmpl"), "x\n")
	writeFile(t, filepath.Join(root, "d", "AGENTS.md.tmpl"), "x\n")
	// 非対象: conf も受け口も無い
	writeFile(t, filepath.Join(root, "e", "README.md"), "x")
	// 非対象: .git 配下は辿らない
	writeFile(t, filepath.Join(root, ".git", "x", TargetConfName), "")
	// 非対象: バンドルはターゲットにしない
	writeFile(t, filepath.Join(root, BundleDirName, "wiki", TargetConfName), "")
	// 非対象: 退避物の置き場
	writeFile(t, filepath.Join(root, archiveDirName, "old", TargetConfName), "")

	got, err := DiscoverTargets(root)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, c := range got {
		rel, err := filepath.Rel(root, c.Dir)
		if err != nil {
			t.Fatal(err)
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	if want := "a,b"; strings.Join(rels, ",") != want {
		t.Errorf("探索結果が不正: want %q, got %q", want, strings.Join(rels, ","))
	}
}

// 既定で配下まで探す。**自身がターゲットでも探索は止めない** — 設定リポジトリの中に
// プロジェクトを並べる構成（<repo>/ と <repo>/sub/ が別ターゲット）を 1 回の apply で拾うため。
func TestDiscoverTargets_自身が該当しても配下も探す(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, TargetConfName), "")
	writeFile(t, filepath.Join(root, "sub", TargetConfName), "")

	got, err := DiscoverTargets(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("自身 + sub の 2 件を期待: %+v", got)
	}
	if got[0].Dir != root {
		t.Errorf("start 自身が含まれていない: %+v", got)
	}
}

// 別リポジトリ（.git を持つ）の中へは降りない。$HOME のような広い場所を指したときに
// 無関係なリポジトリのターゲットまで巻き込むのを防ぐ。
// worktree / submodule では .git がファイルなので、ディレクトリ判定だけでは足りない。
func TestDiscoverTargets_別リポジトリへは降りない(t *testing.T) {
	for _, kind := range []string{"dir", "file"} {
		t.Run(".git が "+kind, func(t *testing.T) {
			root := t.TempDir()
			other := filepath.Join(root, "other-repo")
			writeFile(t, filepath.Join(other, TargetConfName), "")
			if kind == "dir" {
				mkdirAll(t, filepath.Join(other, ".git"))
			} else {
				writeFile(t, filepath.Join(other, ".git"), "gitdir: /elsewhere\n")
			}
			// 同じ階層の .git を持たないディレクトリは拾われる（ガードが効きすぎていないこと）
			writeFile(t, filepath.Join(root, "mine", TargetConfName), "")

			got, err := DiscoverTargets(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("別リポジトリを除いた 1 件を期待: %+v", got)
			}
			if !strings.Contains(got[0].Dir, "mine") {
				t.Errorf("拾ったディレクトリが違う: %+v", got)
			}
		})
	}
}

func TestDiscoverTargets_該当なしは空(t *testing.T) {
	got, err := DiscoverTargets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("空でない: %+v", got)
	}
}

// ---- フラグ解決 -----------------------------------------------------------

func TestFlags_既定を消費側が上書き(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "bundle.conf", "description: wiki 一式\n")
	f.bundleFile(t, "commit", "bundle.conf", "")
	writeFile(t, filepath.Join(f.tplHome, DefaultsConfName), "wiki = true\ncommit = false\n")
	f.confFile(t, "commit = true\n")

	root := f.root(t)
	got, err := root.Flags(Target{Dir: f.projRoot})
	if err != nil {
		t.Fatal(err)
	}
	if !got["wiki"] || !got["commit"] {
		t.Errorf("実効フラグが不正: %+v", got)
	}
	if len(root.Bundles) != 2 {
		t.Errorf("バンドル数が不正: %+v", root.Bundles)
	}
}

// 母集合（全バンドル名）を false で敷くので、conf に書かれていないバンドルも参照できる。
// 存在しない名前は母集合に入らない（タイポ検出は保たれる）。
func TestFlags_母集合が敷かれる(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "bundle.conf", "")
	f.bundleFile(t, "loop", "bundle.conf", "")
	f.confFile(t, "wiki = true\n") // loop はどこにも書かない

	got, err := f.root(t).Flags(Target{Dir: f.projRoot})
	if err != nil {
		t.Fatal(err)
	}
	if !got["wiki"] {
		t.Error("wiki が ON になっていない")
	}
	if v, ok := got["loop"]; !ok || v {
		t.Errorf("conf 未記述のバンドルは false で母集合に入るべき: ok=%v v=%v", ok, v)
	}
	if _, ok := got["typo"]; ok {
		t.Error("存在しないバンドル名が母集合に入っている（タイポ検出が壊れる）")
	}
}

// 断片が conf に書かれていない他フラグを参照しても apply が落ちない（実際に踏んだ事故の再現）。
// 母集合を敷く前は missingkey=error で apply 全体が失敗していた。
func TestRun_断片が未記述のフラグを参照しても落ちない(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "worktree", "CLAUDE.md.tmpl", "\n{{if .commit}}commit あり{{else}}commit なし{{end}}\n")
	f.bundleFile(t, "commit", "bundle.conf", "")
	f.targetFile(t, "CLAUDE.md.tmpl", "頭\n{{- slot \"worktree\"}}\n")
	f.confFile(t, "worktree = true\n") // commit はどこにも書かない

	f.run(t, Options{})
	if got := f.generated(t, "CLAUDE.md"); !strings.Contains(got, "commit なし") {
		t.Errorf("未記述フラグが false として評価されていない:\n%q", got)
	}
}

func TestFlags_未知フラグはエラー(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "bundle.conf", "")
	f.confFile(t, "wikki = true\n")

	if _, err := f.root(t).Flags(Target{Dir: f.projRoot}); err == nil {
		t.Error("未知フラグがエラーになりません")
	}
}

// defaults.conf の検証はルート読み込み時（ターゲットを 1 つも見る前）に済ませる
func TestLoadRoot_defaultsの未知フラグはエラー(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.tplHome, DefaultsConfName), "ghost = true\n")

	if _, err := LoadRoot(f.tplHome); err == nil {
		t.Error("defaults.conf の未知フラグがエラーになりません")
	}
}

// 未設定 = OFF（defaults.conf に全フラグを列挙しなくてよい）
func TestFlags_未設定はOFF(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "bundle.conf", "")

	got, err := f.root(t).Flags(Target{Dir: f.projRoot})
	if err != nil {
		t.Fatal(err)
	}
	if got["wiki"] {
		t.Errorf("未設定は OFF であるべき: %+v", got)
	}
}

// ---- 生成 -----------------------------------------------------------------

// 生成物のバイト列は実行時の cwd に依存してはいけない
// （依存すると別の cwd から実行した check が常に差分を報告し、apply が書き換え続ける）。
func TestApply_生成物はcwdに依存しない(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "CLAUDE.md.tmpl", "本文\n")

	t.Chdir(f.base)
	f.run(t, Options{})
	fromBase := f.generated(t, "CLAUDE.md")

	t.Chdir(t.TempDir()) // まったく別の cwd から
	rep := f.run(t, Options{})
	if rep.Targets[0].Changed {
		t.Error("cwd を変えただけで差分になっている")
	}
	if got := f.generated(t, "CLAUDE.md"); got != fromBase {
		t.Errorf("生成物が cwd で変わった:\n%s\n---\n%s", fromBase, got)
	}
}

// ヘッダの原本パスはバンドルルートの親（= 設定リポジトリのルート）基準
func TestApply_ヘッダの原本パスはリポジトリ相対(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "CLAUDE.md.tmpl", "本文\n")

	f.run(t, Options{})
	head, _, _ := strings.Cut(f.generated(t, "CLAUDE.md"), "\n")
	if !strings.Contains(head, filepath.Join("proj", bundle.DefaultOverlay, "CLAUDE.md.tmpl")) {
		t.Errorf("ヘッダの原本パスが不正: %q", head)
	}
	if strings.Contains(head, f.base) {
		t.Errorf("ヘッダに絶対パスが入っている: %q", head)
	}
}

// 受け口の名前はバンドル名そのもの（{{slot "wiki"}} = wiki バンドルの断片の置き場）。
func TestRun_ON時にslotへ差し込まれOFFで消える(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "CLAUDE.md.tmpl", "- vault の行\n")
	f.targetFile(t, "CLAUDE.md.tmpl", "# 見出し\n\n## メモリ\n- 既存の行\n{{- slot \"wiki\"}}\n")
	writeFile(t, filepath.Join(f.tplHome, DefaultsConfName), "wiki = true\n")

	rep := f.run(t, Options{})
	if len(rep.On) != 1 || rep.On[0] != "wiki" {
		t.Errorf("ON バンドルが不正: %+v", rep.On)
	}
	got := f.generated(t, "CLAUDE.md")
	if !strings.HasPrefix(got, "<!-- GENERATED") {
		t.Errorf("GENERATED ヘッダが無い: %q", got)
	}
	if !strings.Contains(got, "- 既存の行\n- vault の行\n") {
		t.Errorf("slot 差し込みが不正:\n%s", got)
	}

	// OFF にすると消える（空行も残さない）
	writeFile(t, filepath.Join(f.tplHome, DefaultsConfName), "wiki = false\n")
	f.run(t, Options{})
	got = f.generated(t, "CLAUDE.md")
	if strings.Contains(got, "vault の行") {
		t.Errorf("OFF なのに残っている:\n%s", got)
	}
	if !strings.HasSuffix(got, "- 既存の行\n") {
		t.Errorf("OFF 時に余分な行が残っている: %q", got)
	}
}

// 断片は素の Markdown（差し込み先はバンドル名で決まる）。本文はそのまま受け口へ入る。
func TestRun_断片の本文はそのまま差し込まれる(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "commit", "CLAUDE.md.tmpl", "\n## コミット方針\n- 自動コミットする\n")
	f.targetFile(t, "CLAUDE.md.tmpl", "# 見出し\n\n本文\n{{- slot \"commit\"}}\n")
	f.confFile(t, "commit = true\n")

	f.run(t, Options{})
	got := f.generated(t, "CLAUDE.md")
	if !strings.HasSuffix(got, "本文\n\n## コミット方針\n- 自動コミットする\n") {
		t.Errorf("本文がそのまま差し込まれていない:\n%q", got)
	}
}

// 差し込み先のキーはバンドル名なので衝突せず、連結も順序付けも起きない。
// 同じ場所へ複数バンドルを入れたいときは受け口を並べて書き、順序は書き順で決まる。
func TestRun_受け口の書き順で並び順が決まる(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "CLAUDE.md.tmpl", "- wiki の行\n")
	f.bundleFile(t, "commit", "CLAUDE.md.tmpl", "- commit の行\n")
	f.targetFile(t, "CLAUDE.md.tmpl", "頭\n{{- slot \"commit\"}}\n{{- slot \"wiki\"}}\n")
	f.confFile(t, "wiki = true\ncommit = true\n")

	f.run(t, Options{})
	// バンドル名順（commit, wiki）ではなく受け口の書き順に従う
	if got := f.generated(t, "CLAUDE.md"); !strings.HasSuffix(got, "頭\n- commit の行\n- wiki の行\n") {
		t.Errorf("受け口の書き順どおりでない:\n%q", got)
	}
}

// 受け口が無ければ末尾へ追記する。
// これで新フラグの有効化が消費側テンプレの編集なしにフラグ 1 行で済む。
func TestRun_受け口が無ければ末尾へ追記(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "CLAUDE.md.tmpl", "- 行\n")
	f.targetFile(t, "CLAUDE.md.tmpl", "# 受け口なし\n")
	f.confFile(t, "wiki = true\n")

	rep := f.run(t, Options{})
	if got := f.generated(t, "CLAUDE.md"); !strings.HasSuffix(got, "# 受け口なし\n- 行\n") {
		t.Errorf("末尾へ追記されていない:\n%q", got)
	}
	// 受け皿（同名 *.tmpl）はあるので孤児ではない
	if len(rep.Orphans) != 0 {
		t.Errorf("差し込まれたのに孤児として報告された: %+v", rep.Orphans)
	}
}

// **最悪の部分適用を検出する。** ターゲットに同名の *.tmpl が無い断片は差し込み先が存在せず、
// どこにも入らない。それでいてディレクトリ（skills 等）と settings.json は配られるので、
// 「実体と登録は入ったのに指示だけ届かない」状態が exit 0 で成立してしまう。
func TestRun_受け皿が無い断片を孤児として報告する(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "CLAUDE.md.tmpl", "- 届かない行\n")
	f.bundleFile(t, "wiki", "settings.json.tmpl", `{"a":1}`+"\n")
	f.targetFile(t, "settings.json.tmpl", "{}\n") // settings の受け皿だけある
	f.confFile(t, "wiki = true\n")

	rep := f.run(t, Options{})
	if len(rep.Orphans) != 1 {
		t.Fatalf("孤児が 1 件のはず: %+v", rep.Orphans)
	}
	if rep.Orphans[0].Bundle != "wiki" || rep.Orphans[0].File != filepath.Join(bundle.DefaultOverlay, "CLAUDE.md.tmpl") {
		t.Errorf("孤児の内容が不正: %+v", rep.Orphans[0])
	}
	// **差分には数えない**。apply では解消しないので、数えると check が永久に exit 2 になる
	if n := rep.Diffs(); n != 1 { // settings.json の生成 1 件だけ
		t.Errorf("孤児が差分に数えられている: Diffs=%d", n)
	}
}

// OFF のバンドルの断片は「届かない」ではなく「そもそも対象外」なので報告しない
// （毎回出すと警告が無意味になる）。
func TestRun_OFFのバンドルは孤児にしない(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "CLAUDE.md.tmpl", "- 行\n")
	f.targetFile(t, "settings.json.tmpl", "{}\n")
	f.confFile(t, "wiki = false\n")

	if rep := f.run(t, Options{}); len(rep.Orphans) != 0 {
		t.Errorf("OFF のバンドルが孤児として報告された: %+v", rep.Orphans)
	}
}

// 末尾追記と受け口経由でバイト列が一致すること。ここがずれると、受け口を消すだけの
// 移行で生成物に差分が出てしまう（既存 7 消費テンプレはすべて受け口が末尾の 1 行）。
func TestRun_末尾追記は受け口経由とバイト一致(t *testing.T) {
	const body = "頭\n\n## 既存セクション\n- 既存の行\n"
	viaSlot := newFixture(t)
	viaSlot.bundleFile(t, "wiki", "CLAUDE.md.tmpl", "\n## 追加\n- 行\n")
	viaSlot.targetFile(t, "CLAUDE.md.tmpl", body+"{{- slot \"wiki\"}}\n")
	viaSlot.confFile(t, "wiki = true\n")
	viaSlot.run(t, Options{})

	viaAppend := newFixture(t)
	viaAppend.bundleFile(t, "wiki", "CLAUDE.md.tmpl", "\n## 追加\n- 行\n")
	viaAppend.targetFile(t, "CLAUDE.md.tmpl", body)
	viaAppend.confFile(t, "wiki = true\n")
	viaAppend.run(t, Options{})

	if a, b := viaSlot.generated(t, "CLAUDE.md"), viaAppend.generated(t, "CLAUDE.md"); a != b {
		t.Errorf("受け口経由と末尾追記でバイト列が違う:\n受け口:\n%q\n追記:\n%q", a, b)
	}
}

// 受け口が無い追記には「書き順」が存在しないので、規則を決めないと生成物が不安定になる。
// バンドル名の昇順に固定する。
func TestRun_末尾追記の順序はバンドル名昇順(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "CLAUDE.md.tmpl", "- wiki の行\n")
	f.bundleFile(t, "commit", "CLAUDE.md.tmpl", "- commit の行\n")
	f.targetFile(t, "CLAUDE.md.tmpl", "頭\n")
	f.confFile(t, "wiki = true\ncommit = true\n")

	f.run(t, Options{})
	if got := f.generated(t, "CLAUDE.md"); !strings.HasSuffix(got, "頭\n- commit の行\n- wiki の行\n") {
		t.Errorf("バンドル名の昇順で追記されていない:\n%q", got)
	}
}

// 差し込み先はバンドル名そのものなので、独立した語彙ファイル（旧 slots.conf）が無くても
// 受け口のタイポ・旧語彙の残りを「そんなバンドルは無い」として弾ける。
func TestRun_受け口名がバンドル名でなければエラー(t *testing.T) {
	for _, name := range []string{"memory", "wikki"} { // 旧語彙の残り / 単純なタイポ
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.bundleFile(t, "wiki", "bundle.conf", "")
			f.targetFile(t, "CLAUDE.md.tmpl", "頭\n{{- slot \""+name+"\"}}\n")

			err := f.applyErr(t)
			if err == nil {
				t.Fatal("バンドル名でない受け口がエラーになりません")
			}
			if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), msg.Lit(msg.M.Apply.UnknownSlot)) ||
				!strings.Contains(err.Error(), "wiki") {
				t.Errorf("メッセージが不正: %v", err)
			}
		})
	}
}

// バンドルが 1 つも無ければどの受け口も差し込み先を持たない（検証を素通しさせない）
func TestRun_バンドルが無ければ受け口はエラー(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "CLAUDE.md.tmpl", "頭\n{{- slot \"wiki\"}}\n")

	err := f.applyErr(t)
	if err == nil {
		t.Fatal("差し込み先の無い受け口がエラーになりません")
	}
	if !strings.Contains(err.Error(), "wiki") {
		t.Errorf("メッセージが不正: %v", err)
	}
}

func TestRun_フラグ横断条件が効く(t *testing.T) {
	f := newFixture(t)
	// loop 断片は wiki が ON のときだけ行を出す
	f.bundleFile(t, "loop", "CLAUDE.md.tmpl", "{{if .wiki}}- wiki 併用時の行{{end}}\n")
	f.bundleFile(t, "wiki", "bundle.conf", "")
	f.targetFile(t, "CLAUDE.md.tmpl", "頭\n{{- slot \"loop\"}}\n")
	f.confFile(t, "loop = true\nwiki = false\n")

	f.run(t, Options{})
	if got := f.generated(t, "CLAUDE.md"); strings.Contains(got, "wiki 併用時の行") {
		t.Errorf("wiki=false なのに出ている:\n%s", got)
	}
	// 空の寄稿なので空行も残らない
	if got := f.generated(t, "CLAUDE.md"); !strings.HasSuffix(got, "頭\n") {
		t.Errorf("空寄稿で余分な行が残る: %q", got)
	}

	f.confFile(t, "loop = true\nwiki = true\n")
	f.run(t, Options{})
	if got := f.generated(t, "CLAUDE.md"); !strings.Contains(got, "wiki 併用時の行") {
		t.Errorf("wiki=true で出ていない:\n%s", got)
	}
}

func TestRun_冪等(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "CLAUDE.md.tmpl", "本文\n")
	f.confFile(t, "")

	rep := f.run(t, Options{})
	if !rep.Targets[0].Changed {
		t.Error("初回は Changed=true であるべき")
	}
	rep = f.run(t, Options{})
	if rep.Targets[0].Changed {
		t.Error("2 回目は Changed=false であるべき（冪等）")
	}
}

func TestRun_DryRunは書かない(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "CLAUDE.md.tmpl", "本文\n")

	rep := f.run(t, Options{DryRun: true})
	if !rep.Targets[0].Changed {
		t.Error("DryRun でも差分は報告すべき")
	}
	if _, err := os.Stat(filepath.Join(f.target, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("DryRun なのに生成物が書かれた")
	}
}

func TestRun_手書き生成物は退避される(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "CLAUDE.md.tmpl", "生成本文\n")
	f.targetFile(t, "CLAUDE.md", "手で書いた本文\n")

	rep := f.run(t, Options{})
	if rep.Targets[0].Archived == "" {
		t.Fatal("手書き実体が退避されていない")
	}
	// 既定の退避先はターゲット直下（バンドルが別リポジトリでも退避物は手元に残る）
	if !strings.HasPrefix(rep.Targets[0].Archived, filepath.Join(f.projRoot, ".archive")) {
		t.Errorf("退避先が不正: %s", rep.Targets[0].Archived)
	}
}

// ---- JSON（settings.json）の deep merge ----------------------------------

func TestRun_JSONはdeepMergeされる(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "loop", "settings.json.tmpl",
		`{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "verify"}]}]},`+
			`"permissions": {"allow": ["Bash(go *)"]}}`)
	f.targetFile(t, "settings.json.tmpl",
		`{"sandbox": {"enabled": false}, "permissions": {"allow": ["Bash(make *)"]}}`)
	f.confFile(t, "loop = true\n")

	f.run(t, Options{})
	got := f.generated(t, "settings.json")

	for _, want := range []string{`"command": "verify"`, `"Bash(go *)"`, `"Bash(make *)"`, `"enabled": false`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s が生成物に無い:\n%s", want, got)
		}
	}
	// JSON として妥当で、キーはソート済み（決定的）
	var v map[string]any
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("生成物が不正な JSON: %v\n%s", err, got)
	}
	if strings.Index(got, `"hooks"`) > strings.Index(got, `"permissions"`) {
		t.Errorf("キーがソートされていない:\n%s", got)
	}
	// Markdown 用の GENERATED ヘッダは入れない（JSON が壊れる）
	if strings.HasPrefix(got, "<!--") {
		t.Errorf("JSON にヘッダが入っている:\n%s", got)
	}
}

func TestRun_JSONは消費側が後勝ち(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "loop", "settings.json.tmpl", `{"sandbox": {"enabled": true}}`)
	f.targetFile(t, "settings.json.tmpl", `{"sandbox": {"enabled": false}}`)
	f.confFile(t, "loop = true\n")

	f.run(t, Options{})
	if got := f.generated(t, "settings.json"); !strings.Contains(got, `"enabled": false`) {
		t.Errorf("消費側が勝っていない:\n%s", got)
	}
}

func TestRun_不正なJSON断片は出典付きでエラー(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "loop", "settings.json.tmpl", `{"hooks": }`)
	f.targetFile(t, "settings.json.tmpl", `{}`)
	f.confFile(t, "loop = true\n")

	err := f.applyErr(t)
	if err == nil {
		t.Fatal("不正 JSON がエラーになりません")
	}
	if !strings.Contains(err.Error(), "loop") {
		t.Errorf("どのバンドルかがメッセージに無い: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.target, "settings.json")); !os.IsNotExist(err) {
		t.Error("失敗時に生成物を書いてしまっている")
	}
}

func TestRun_JSONは冪等でstateにハッシュを記録する(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "settings.json.tmpl", `{"a": 1}`)

	rep := f.run(t, Options{})
	if !rep.Targets[0].Changed {
		t.Error("初回は Changed=true であるべき")
	}
	statePath := filepath.Join(f.projRoot, state.FileName)
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state ファイルが無い: %v", err)
	}
	st, err := state.Load(f.projRoot)
	if err != nil {
		t.Fatal(err)
	}
	// キーは**ターゲット相対パス**（v2）。basename ではない
	if st.Get(filepath.Join(bundle.DefaultOverlay, "settings.json")) == "" {
		t.Errorf("state にハッシュが記録されていない: %+v", st.Generated)
	}

	rep = f.run(t, Options{})
	if rep.Targets[0].Changed {
		t.Error("2 回目は Changed=false であるべき（冪等）")
	}
	if rep.Targets[0].Archived != "" {
		t.Errorf("自分の生成物を退避してはいけない: %s", rep.Targets[0].Archived)
	}
}

// Claude Code 自身（/model 等）が書き換えた settings.json は黙って上書きせず退避する
func TestRun_JSONの外部書き換えは退避される(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "settings.json.tmpl", `{"a": 1}`)
	f.run(t, Options{})

	f.targetFile(t, "settings.json", `{"a": 1, "theme": "dark"}`) // 外部の書き込み
	rep := f.run(t, Options{})

	if rep.Targets[0].Archived == "" {
		t.Fatal("外部の書き込みが退避されていない")
	}
	b, err := os.ReadFile(rep.Targets[0].Archived)
	if err != nil || !strings.Contains(string(b), "theme") {
		t.Errorf("退避内容が不正: %v", err)
	}
}

func TestRun_JSONのDryRunは書かない(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "settings.json.tmpl", `{"a": 1}`)

	rep := f.run(t, Options{DryRun: true})

	if !rep.Targets[0].Changed {
		t.Error("DryRun でも差分は報告すべき")
	}
	if _, err := os.Stat(filepath.Join(f.target, "settings.json")); !os.IsNotExist(err) {
		t.Error("DryRun なのに生成物が書かれた")
	}
	if _, err := os.Stat(filepath.Join(f.target, state.FileName)); !os.IsNotExist(err) {
		t.Error("DryRun なのに state が書かれた")
	}
}

// .claude/CLAUDE.local.md は Claude Code が読まない位置なので、生成せず明示的に止める
func TestRun_CLAUDE_local_mdは非対応エラー(t *testing.T) {
	f := newFixture(t)
	f.targetFile(t, "CLAUDE.local.md.tmpl", "x\n")

	err := f.applyErr(t)
	if err == nil {
		t.Fatal("エラーを期待したが nil")
	}
	if !strings.Contains(err.Error(), "CLAUDE.local.md") {
		t.Errorf("メッセージが不正: %v", err)
	}
}

// Claude Code の Local instructions はリポジトリルート直下の CLAUDE.local.md なので、
// テンプレをそこへ置けば生成できる（読まれないのは .claude/ 配下だけ）。
// ガードはディレクトリ名で決まるので、ターゲットをリポジトリルートにすれば通る。
func TestRun_CLAUDE_local_mdはリポジトリルートなら生成できる(t *testing.T) {
	f := newFixture(t)
	repoRoot := filepath.Dir(f.target)
	writeFile(t, filepath.Join(repoRoot, "CLAUDE.local.md.tmpl"), "個人メモ\n")

	rep, err := f.root(t).Apply(Target{Dir: repoRoot}, Options{})
	if err != nil {
		t.Fatalf("Apply が失敗: %v", err)
	}

	dest := filepath.Join(repoRoot, "CLAUDE.local.md")
	if rep.Targets[0].Dest != dest {
		t.Errorf("生成先がリポジトリルートでない: want %s, got %s", dest, rep.Targets[0].Dest)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("生成されていない: %v", err)
	}
	if !strings.Contains(string(b), "個人メモ") {
		t.Errorf("本文が入っていない: %s", b)
	}
}

// settings.local.json は Claude Code が /model・/permissions で書き込むライブファイルだが、
// .tmpl を置いたターゲットは生成物として宣言的に管理できる（opt-in・2026-08-20 に方針転換）。
// バンドル断片との deep merge も settings.json と同じに効く。
func TestRun_settings_local_jsonは生成できてバンドル断片とマージされる(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "loop", "settings.local.json.tmpl",
		`{"permissions": {"allow": ["Bash(go *)"]}}`)
	f.targetFile(t, "settings.local.json.tmpl",
		`{"permissions": {"allow": ["Bash(make *)"], "deny": ["Bash(git push *)"]}}`)
	f.confFile(t, "loop = true\n")

	f.run(t, Options{})
	got := f.generated(t, "settings.local.json")

	for _, want := range []string{`"Bash(go *)"`, `"Bash(make *)"`, `"Bash(git push *)"`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s が生成物に無い:\n%s", want, got)
		}
	}
}

func TestRun_消費側partialsがバンドルより優先(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "partials/x.tmpl", "バンドル版")
	f.targetFile(t, "partials/x.tmpl", "消費側版")
	f.targetFile(t, "CLAUDE.md.tmpl", "{{template \"x.tmpl\" .}}\n")
	f.confFile(t, "wiki = true\n")

	f.run(t, Options{})
	if got := f.generated(t, "CLAUDE.md"); !strings.Contains(got, "消費側版") {
		t.Errorf("後勝ちになっていない:\n%s", got)
	}
}

// ---- リンク合成（internal/link との結合） --------------------------------

func TestRun_ONバンドルのrulesとskillsがリンクされOFFで消える(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "loop", "rules/loop-engineering.md", "規約\n")
	f.bundleFile(t, "loop", "skills/loop-check/SKILL.md", "s\n")
	f.confFile(t, "loop = true\n")

	rep := f.run(t, Options{})
	if len(rep.Links) == 0 {
		t.Fatal("リンク合成が実行されていない")
	}
	rulesLink := filepath.Join(f.target, "rules", "loop")
	if _, err := os.Readlink(rulesLink); err != nil {
		t.Errorf(".claude/rules/loop が張られていない: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(f.target, "skills", "loop-check")); err != nil {
		t.Errorf(".claude/skills/loop-check が張られていない: %v", err)
	}

	// OFF にすると外れる
	f.confFile(t, "loop = false\n")
	f.run(t, Options{})
	if _, err := os.Lstat(rulesLink); !os.IsNotExist(err) {
		t.Error("OFF なのに rules のリンクが残っている")
	}
}

func TestRun_DryRunではリンクも張らない(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "loop", "rules/x.md", "x\n")
	f.confFile(t, "loop = true\n")

	rep := f.run(t, Options{DryRun: true})

	if len(rep.Links) == 0 {
		t.Error("DryRun でも作成予定を報告すべき")
	}
	if _, err := os.Lstat(filepath.Join(f.target, "rules", "loop")); !os.IsNotExist(err) {
		t.Error("DryRun なのにリンクが作られた")
	}
}

func TestRun_テンプレが無ければ生成物なし(t *testing.T) {
	f := newFixture(t)
	f.confFile(t, "")

	rep := f.run(t, Options{})
	if len(rep.Targets) != 0 {
		t.Errorf("生成対象は 0 であるべき: %+v", rep.Targets)
	}
}

// ---- ターゲットの制約と退避ラベル -----------------------------------

// バンドル断片も「*.tmpl を持つディレクトリ」の条件を満たすため、直接指定を弾く
// （-r 探索では llm-tpl/ へ降りないので、素通しになるのは明示指定のときだけ）。
func TestApply_バンドル配下はターゲットにできない(t *testing.T) {
	f := newFixture(t)
	f.bundleFile(t, "wiki", "CLAUDE.md.tmpl", "断片\n")

	_, err := f.root(t).Apply(Target{Dir: filepath.Join(f.tplHome, "wiki")}, Options{})
	if err == nil {
		t.Fatal("エラーを期待したが nil")
	}
	if !strings.Contains(err.Error(), msg.Lit(msg.M.Apply.TargetUnderRoot)) {
		t.Errorf("理由が示されていない: %v", err)
	}
}

// 退避先はターゲット直下。生成物の basename が同じターゲットが複数あっても、
// 退避物は各ターゲットの中に残るので、どのプロジェクトの手編集かがパスで分かる。
func TestApply_退避物はターゲットごとに分かれる(t *testing.T) {
	f := newFixture(t)
	r := f.root(t)

	dirs := []string{f.projRoot, filepath.Join(f.base, "other")}
	var archived []string
	for _, d := range dirs {
		ov := filepath.Join(d, bundle.DefaultOverlay)
		writeFile(t, filepath.Join(ov, "CLAUDE.md.tmpl"), "本文\n")
		writeFile(t, filepath.Join(ov, "CLAUDE.md"), "手で書いた\n") // 生成物でない実体
		rep, err := r.Apply(Target{Dir: d}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if rep.Targets[0].Archived == "" {
			t.Fatalf("%s: 手書き生成物が退避されていない", d)
		}
		archived = append(archived, rep.Targets[0].Archived)
	}

	for i, d := range dirs {
		if !strings.HasPrefix(archived[i], filepath.Join(d, ".archive")) {
			t.Errorf("退避物が %s の外へ出た: %s", d, archived[i])
		}
	}
	if archived[0] == archived[1] {
		t.Error("2 つのターゲットの退避物が同じパスへ潰れた")
	}
}
