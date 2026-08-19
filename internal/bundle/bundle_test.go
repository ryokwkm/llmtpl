package bundle

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover_ディレクトリだけをソート順で返す(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "wiki")
	mkdir(t, root, "commit")
	mkdir(t, root, "loop")
	mkdir(t, root, ".git")                     // ドット始まりは除外
	write(t, root, "defaults.conf", "x = 1\n") // ファイルは除外
	write(t, root, "README.md", "x")

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover が失敗: %v", err)
	}
	want := []string{"commit", "loop", "wiki"}
	if len(got) != len(want) {
		t.Fatalf("件数が不正: %+v", got)
	}
	for i, b := range got {
		if b.Name != want[i] {
			t.Errorf("[%d] 名前が不正: %q（want %q）", i, b.Name, want[i])
		}
		if b.Dir != filepath.Join(root, want[i]) {
			t.Errorf("[%d] Dir が不正: %q", i, b.Dir)
		}
	}
}

func TestDiscover_空ルートは空(t *testing.T) {
	got, err := Discover(t.TempDir())
	if err != nil {
		t.Fatalf("Discover が失敗: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("空でない: %+v", got)
	}
}

func TestDiscover_ルートが無ければエラー(t *testing.T) {
	if _, err := Discover(filepath.Join(t.TempDir(), "no-such")); err == nil {
		t.Error("存在しないバンドルルートがエラーになりません")
	}
}

// バンドルの実体を別リポジトリに置き、ルートには symlink だけを並べる構成を支える
// （会社リポジトリの固有バンドルと、共有リポジトリのバンドルを 1 ルートに混ぜる用途）。
func TestDiscover_symlinkのバンドルも拾う(t *testing.T) {
	base := t.TempDir()
	root := mkdir(t, base, "root")
	other := mkdir(t, base, "other", "shared")
	write(t, other, "bundle.conf", "description: 別リポジトリの実体\n")
	mkdir(t, root, "own")
	if err := os.Symlink(other, filepath.Join(root, "shared")); err != nil {
		t.Fatal(err)
	}
	// ファイルへの symlink はバンドルではない（辿った先がディレクトリでないもの）
	write(t, base, "plain.txt", "x")
	if err := os.Symlink(filepath.Join(base, "plain.txt"), filepath.Join(root, "notabundle")); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover が失敗: %v", err)
	}
	want := []string{"own", "shared"}
	if len(got) != len(want) {
		t.Fatalf("件数が不正: %+v", got)
	}
	for i, b := range got {
		if b.Name != want[i] {
			t.Errorf("[%d] 名前が不正: %q（want %q）", i, b.Name, want[i])
		}
	}
	// Dir は symlink のパスのまま（実体解決しない）。link 側の所有権判定が
	// 「バンドルルート配下を指すか」を字面で見るため、ここで解決すると所有と見なされなくなる
	if got[1].Dir != filepath.Join(root, "shared") {
		t.Errorf("Dir が実体へ解決されています: %q", got[1].Dir)
	}
}

func TestDistDirs_symlinkのディレクトリも拾う(t *testing.T) {
	base := t.TempDir()
	b := Bundle{Name: "x", Dir: mkdir(t, base, "x")}
	realRules := mkdir(t, base, "elsewhere", "rules")
	write(t, realRules, "a.md", "# a")
	if err := os.Symlink(realRules, filepath.Join(b.Dir, "rules")); err != nil {
		t.Fatal(err)
	}

	got, err := DistDirs(b)
	if err != nil {
		t.Fatalf("DistDirs が失敗: %v", err)
	}
	if len(got) != 1 || got[0] != "rules" {
		t.Errorf("symlink の rules を拾えていません: %+v", got)
	}

	// 表示側（Contents）も同じ判定であること（食い違うと「出るのに配られない」を招く）
	contents, err := Contents(b)
	if err != nil {
		t.Fatalf("Contents が失敗: %v", err)
	}
	if len(contents) != 1 || contents[0] != "rules/" {
		t.Errorf("Contents がディレクトリとして扱っていません: %+v", contents)
	}
}

func TestNames(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "b")
	mkdir(t, root, "a")

	bundles, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	got := Names(bundles)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("Names が不正: %v", got)
	}
}

// Compose の契約: slot の内容は「先頭 \n・末尾改行なし」。
// 消費側が {{- slot "name"}} を専用行に置くと、寄稿ゼロのとき空行も残らない。
// キーはバンドル名（= 差し込み先の slot 名）なので衝突せず、連結も順序付けも無い。
func TestCompose_バンドル名がslotのキーになる(t *testing.T) {
	got := Compose([]Piece{
		{Bundle: "wiki", Text: "- wiki の行\n"},
		{Bundle: "loop", Text: "- loop の行\n"},
	})
	if len(got.Slots) != 2 {
		t.Fatalf("件数が不正: %+v", got.Slots)
	}
	if got.Slots["wiki"] != "\n- wiki の行" {
		t.Errorf("wiki の内容が不正: %q", got.Slots["wiki"])
	}
	if got.Slots["loop"] != "\n- loop の行" {
		t.Errorf("loop の内容が不正: %q", got.Slots["loop"])
	}
}

// 複数行・末尾に改行が複数ある断片でも「先頭 \n・末尾改行なし」に正規化する。
func TestCompose_末尾改行を落として先頭に改行を付ける(t *testing.T) {
	got := Compose([]Piece{
		{Bundle: "commit", Text: "## コミット方針\n- 自動コミット\n\n"},
	})
	if got.Slots["commit"] != "\n## コミット方針\n- 自動コミット" {
		t.Errorf("正規化が不正: %q", got.Slots["commit"])
	}
}

// フラグ横断条件（{{if .other}}）で中身が消えた断片は、寄稿ゼロとして扱う。
// ここを「空文字を入れる」にすると余分な空行が生成物に残る。
func TestCompose_空の断片は寄稿しない(t *testing.T) {
	got := Compose([]Piece{
		{Bundle: "empty", Text: "\n\n"},
		{Bundle: "blank", Text: "   \n"},
		{Bundle: "real", Text: "- 実体\n"},
	})
	if len(got.Slots) != 1 {
		t.Fatalf("空断片が混ざっています: %+v", got.Slots)
	}
	if got.Slots["real"] != "\n- 実体" {
		t.Errorf("内容が不正: %q", got.Slots["real"])
	}
	if _, ok := got.Slots["empty"]; ok {
		t.Error("空断片のキーが作られています（受け口が空文字で埋まると空行が残る）")
	}
}

func TestCompose_空入力(t *testing.T) {
	got := Compose(nil)
	if got.Slots == nil {
		t.Error("Slots は非 nil の空 map であるべき（参照側で nil チェックを要求しない）")
	}
	if len(got.Slots) != 0 {
		t.Errorf("空入力の結果が不正: %+v", got)
	}
}

func TestLoadFragment(t *testing.T) {
	root := t.TempDir()
	dir := mkdir(t, root, "wiki")
	write(t, dir, "CLAUDE.md.tmpl", "- vault の行\n")

	b := Bundle{Name: "wiki", Dir: dir}

	content, ok, err := LoadFragment(b, "CLAUDE.md.tmpl")
	if err != nil || !ok {
		t.Fatalf("読めていない: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(string(content), "vault の行") {
		t.Errorf("内容が不正: %q", content)
	}

	// 無い断片は ok=false（エラーではない。バンドルは断片を持たなくてよい）
	if _, ok, err := LoadFragment(b, "settings.json.tmpl"); err != nil || ok {
		t.Errorf("存在しない断片の扱いが不正: ok=%v err=%v", ok, err)
	}
}

func TestLoadMeta_bundleConfから読む(t *testing.T) {
	root := t.TempDir()
	dir := mkdir(t, root, "wiki")
	write(t, dir, "bundle.conf", "# メモ\ndescription: Obsidian wiki 一式\n")

	got, err := LoadMeta(Bundle{Name: "wiki", Dir: dir})
	if err != nil {
		t.Fatalf("失敗: %v", err)
	}
	if got.Description != "Obsidian wiki 一式" {
		t.Errorf("description が不正: %q", got.Description)
	}
	// bundle.conf 無しも既定値（任意ファイル）
	b2 := Bundle{Name: "commit", Dir: mkdir(t, root, "commit")}
	if got, err := LoadMeta(b2); err != nil || got.Description != "" {
		t.Errorf("bundle.conf 無しの扱いが不正: %+v %v", got, err)
	}
}

// 使えるキーは description だけ。廃止した placement を含め、未知のキーは黙って無視せずエラーにする
// （無視すると「書いたのに効かない」に気づけない）。
func TestLoadMeta_未知のキーはエラー(t *testing.T) {
	root := t.TempDir()
	dir := mkdir(t, root, "b")
	write(t, dir, "bundle.conf", "description: x\nplacement: opt-in\n")

	_, err := LoadMeta(Bundle{Name: "b", Dir: dir})
	if err == nil {
		t.Fatal("未知のキーがエラーになりません")
	}
	if !strings.Contains(err.Error(), "placement") || !strings.Contains(err.Error(), "description") {
		t.Errorf("原因のキーと使えるキーがメッセージに無い: %v", err)
	}
}

// Fragments は「このバンドルが寄稿しうる先」の一覧。ターゲットに受け皿が無い断片を
// apply が検出するための入力なので、配布物（ディレクトリ）やメタファイルを混ぜない。
func TestFragments_トップレベルのtmplだけを返す(t *testing.T) {
	root := t.TempDir()
	dir := mkdir(t, root, "wiki")
	write(t, dir, "CLAUDE.md.tmpl", "x\n")
	write(t, dir, "settings.json.tmpl", "{}\n")
	write(t, dir, "bundle.conf", "description: x\n")
	write(t, dir, "THIRD_PARTY.md", "x\n") // tmpl でない
	write(t, mkdir(t, dir, "skills"), "SKILL.md", "x\n")
	write(t, mkdir(t, dir, PartialsDirName), "part.tmpl", "x\n") // 部品は寄稿先を持たない

	got, err := Fragments(Bundle{Name: "wiki", Dir: dir})
	if err != nil {
		t.Fatalf("失敗: %v", err)
	}
	want := []string{"CLAUDE.md.tmpl", "settings.json.tmpl"}
	if !slices.Equal(got, want) {
		t.Errorf("Fragments が不正: %v (want %v)", got, want)
	}
}

// 表示（Contents）と配布（DistDirs）で除外規則が食い違うと
// 「llmtpl flags には出るが配られない」が起きるので、同じ判定を使うことを固定する。
func TestContents_配布されないものは出さない(t *testing.T) {
	root := t.TempDir()
	dir := mkdir(t, root, "wiki")
	write(t, dir, ConfName, "description: x\n")     // メタファイル
	write(t, dir, PartialsDirName+"/x.tmpl", "x\n") // 部品（配布対象外）
	write(t, dir, ".DS_Store", "x")                 // ドット始まり
	write(t, dir, "CLAUDE.md.tmpl", "x\n")
	write(t, dir, "skills/s/SKILL.md", "x\n")

	got, err := Contents(Bundle{Name: "wiki", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "CLAUDE.md.tmpl" || got[1] != "skills/" {
		t.Errorf("一覧が不正（partials / bundle.conf / ドット始まりは出さない）: %v", got)
	}
}

func TestDistDirs_配布対象のディレクトリだけ(t *testing.T) {
	root := t.TempDir()
	dir := mkdir(t, root, "loop")
	write(t, dir, ConfName, "")
	write(t, dir, "CLAUDE.md.tmpl", "x\n") // ファイルは対象外
	write(t, dir, PartialsDirName+"/x.tmpl", "x\n")
	write(t, dir, "rules/a.md", "x\n")
	write(t, dir, "hooks/a.sh", "x\n")

	got, err := DistDirs(Bundle{Name: "loop", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "hooks" || got[1] != "rules" {
		t.Errorf("配布対象が不正: %v", got)
	}
}

func TestContents_読めなければエラー(t *testing.T) {
	if _, err := Contents(Bundle{Name: "x", Dir: filepath.Join(t.TempDir(), "no-such")}); err == nil {
		t.Error("読めないディレクトリがエラーになりません（黙って空を返してはいけない）")
	}
}

func TestLoadMeta_未知キーはエラー(t *testing.T) {
	root := t.TempDir()
	dir := mkdir(t, root, "wiki")
	write(t, dir, "bundle.conf", "desc: 短縮形は許さない\n")

	if _, err := LoadMeta(Bundle{Name: "wiki", Dir: dir}); err == nil {
		t.Error("未知キーがエラーになりません")
	}
}

// --- オーバーレイ層（バンドルの中身がターゲットの中へそのまま重なる）---

// mkOverlayBundle は「新レイアウトのバンドル」を 1 つ作って返す。
func mkOverlayBundle(t *testing.T) Bundle {
	t.Helper()
	root := t.TempDir()
	dir := mkdir(t, root, "logcheck")
	write(t, dir, "AGENTS.md.tmpl", "agents")
	write(t, dir, ".claude/CLAUDE.md.tmpl", "claude")
	write(t, dir, ".claude/settings.json.tmpl", "{}")
	write(t, dir, ".claude/rules/format.md", "rule")
	write(t, dir, ".claude/hooks/verify.sh", "#!/bin/sh")
	write(t, dir, "bundle.conf", "description: x")
	return Bundle{Name: "logcheck", Dir: dir}
}

func TestEntries_オーバーレイの中身をバンドル相対パスで返す(t *testing.T) {
	got, err := entries(mkOverlayBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude/CLAUDE.md.tmpl", ".claude/hooks", ".claude/rules", ".claude/settings.json.tmpl", "AGENTS.md.tmpl"}
	if !slices.Equal(got, want) {
		t.Errorf("entries()\n got: %v\nwant: %v", got, want)
	}
}

func TestEntries_オーバーレイ自身は配布要素に出ない(t *testing.T) {
	got, err := entries(mkOverlayBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	// .claude を配布要素として返すと <T>/.claude が丸ごと symlink に置き換わりターゲットを乗っ取る。
	if slices.Contains(got, ".claude") {
		t.Errorf("オーバーレイ自身が配布要素に出ている: %v", got)
	}
}

func TestEntries_オーバーレイ内のドット始まりも除外する(t *testing.T) {
	b := mkOverlayBundle(t)
	write(t, b.Dir, ".claude/.DS_Store", "junk")
	write(t, b.Dir, ".claude/partials/part.tmpl", "p")
	write(t, b.Dir, ".claude/bundle.conf", "description: y")
	got, err := entries(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{".claude/.DS_Store", ".claude/partials", ".claude/bundle.conf"} {
		if slices.Contains(got, bad) {
			t.Errorf("%s が配布要素に出ている: %v", bad, got)
		}
	}
}

func TestEntries_一般のサブディレクトリへは降りない(t *testing.T) {
	b := mkOverlayBundle(t)
	write(t, b.Dir, ".claude/rules/nested/deep.md", "deep")
	got, err := entries(b)
	if err != nil {
		t.Fatal(err)
	}
	// rules の中は器ごと symlink するので、中身まで列挙してはいけない。
	if slices.Contains(got, ".claude/rules/nested") {
		t.Errorf("器の中まで降りている: %v", got)
	}
}

func TestFragments_2層の同名tmplが共存できる(t *testing.T) {
	b := mkOverlayBundle(t)
	write(t, b.Dir, ".claude/AGENTS.md.tmpl", "claude 側の AGENTS")
	got, err := Fragments(b)
	if err != nil {
		t.Fatal(err)
	}
	// basename 照合だと衝突していた 2 つが、相対パスなので別物として並ぶ。
	for _, want := range []string{"AGENTS.md.tmpl", ".claude/AGENTS.md.tmpl"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s が Fragments に無い: %v", want, got)
		}
	}
}

func TestDistDirs_オーバーレイ配下の器を相対パスで返す(t *testing.T) {
	got, err := DistDirs(mkOverlayBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude/hooks", ".claude/rules"}
	if !slices.Equal(got, want) {
		t.Errorf("DistDirs()\n got: %v\nwant: %v", got, want)
	}
}

func TestContents_オーバーレイ配下も相対パスで表示する(t *testing.T) {
	got, err := Contents(mkOverlayBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude/CLAUDE.md.tmpl", ".claude/hooks/", ".claude/rules/", ".claude/settings.json.tmpl", "AGENTS.md.tmpl"}
	if !slices.Equal(got, want) {
		t.Errorf("Contents()\n got: %v\nwant: %v", got, want)
	}
}

func TestLoadFragment_相対パスの断片を読める(t *testing.T) {
	b := mkOverlayBundle(t)
	content, ok, err := LoadFragment(b, ".claude/CLAUDE.md.tmpl")
	if err != nil || !ok {
		t.Fatalf("読めない: ok=%v err=%v", ok, err)
	}
	if string(content) != "claude" {
		t.Errorf("中身が違う: %q", content)
	}
}

func TestCheckLayout_新レイアウトは通る(t *testing.T) {
	if err := CheckLayout(mkOverlayBundle(t)); err != nil {
		t.Errorf("新レイアウトが弾かれた: %v", err)
	}
}

func TestCheckLayout_バンドル直下のディレクトリはエラー(t *testing.T) {
	b := mkOverlayBundle(t)
	write(t, b.Dir, "rules/old.md", "旧レイアウト")
	err := CheckLayout(b)
	if err == nil {
		t.Fatal("バンドル直下の rules/ が通ってしまった")
	}
	// 移行先を名指しできていないとユーザーが直せない。
	if !strings.Contains(err.Error(), ".claude/rules/") {
		t.Errorf("移行先が示されていない: %v", err)
	}
}

func TestCheckLayout_ルート直下のファイル断片は許す(t *testing.T) {
	root := t.TempDir()
	dir := mkdir(t, root, "agents-only")
	write(t, dir, "AGENTS.md.tmpl", "x")
	write(t, dir, "THIRD_PARTY.md", "notice")
	if err := CheckLayout(Bundle{Name: "agents-only", Dir: dir}); err != nil {
		t.Errorf("ルート直下のファイルが弾かれた: %v", err)
	}
}

func TestCheckLayout_partialsとオーバーレイは対象外(t *testing.T) {
	b := mkOverlayBundle(t)
	write(t, b.Dir, "partials/part.tmpl", "p")
	if err := CheckLayout(b); err != nil {
		t.Errorf("partials / .claude が弾かれた: %v", err)
	}
}

// --- 層の一般化（.claude を特別扱いしない）---

func TestLayers_棚の全バンドルから集めて既定を必ず含む(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "a")
	write(t, a, ".claude/rules/x.md", "x")
	b := mkdir(t, root, "b")
	write(t, b, ".cursor/rules/y.md", "y")
	c := mkdir(t, root, "c")
	write(t, c, ".github/workflows/z.yml", "z")

	bundles, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Layers(bundles)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude", ".cursor", ".github"}
	if !slices.Equal(got, want) {
		t.Errorf("Layers()\n got: %v\nwant: %v", got, want)
	}
}

// バンドルが 1 つも層を持たなくても .claude は残す。
// 残さないと、最後のバンドルを消した瞬間に以前配ったリンクを回収できなくなる。
func TestLayers_バンドルが空でも既定の層は残る(t *testing.T) {
	got, err := Layers(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{DefaultOverlay}) {
		t.Errorf("Layers(nil) = %v, want [%s]", got, DefaultOverlay)
	}
}

// バンドル自体が git リポジトリでも .git の中は舐めない。
func TestLayers_gitは層にしない(t *testing.T) {
	root := t.TempDir()
	b := mkdir(t, root, "vendored")
	write(t, b, ".git/config", "x")
	write(t, b, ".claude/rules/r.md", "r")

	bundles, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Layers(bundles)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(got, ".git") {
		t.Errorf(".git が層に入っている: %v", got)
	}
}

// これが最初に静かに落ちていた経路。.claude 以外の層も同じ規則で配る。
func TestEntries_claude以外の層も列挙する(t *testing.T) {
	root := t.TempDir()
	dir := mkdir(t, root, "hoge")
	write(t, dir, ".claude/test/bbbb.md", "B")
	write(t, dir, ".hogehoge/test/aaaa.md", "A")

	got, err := entries(Bundle{Name: "hoge", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude/test", ".hogehoge/test"}
	if !slices.Equal(got, want) {
		t.Errorf("entries()\n got: %v\nwant: %v", got, want)
	}
}
