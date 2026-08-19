package link

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryokwkm/llmtpl/internal/bundle"
)

type fixture struct {
	root    string
	tplHome string
	target  string // ターゲット（プロジェクトルート）= Sync の outDir 引数
	outDir  string // <target>/.claude = リンクが落ちる場所
	archive string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{
		root:    root,
		tplHome: filepath.Join(root, "llm-tpl"),
		// target = ターゲット（プロジェクトルート）。Sync に渡すのはこちら。
		// outDir = リンクが実際に落ちる場所。ここは新旧モデルで動かないので assertion は不変。
		target:  filepath.Join(root, "proj"),
		outDir:  filepath.Join(root, "proj", ".claude"),
		archive: filepath.Join(root, ".archive"),
	}
	mkdir(t, f.tplHome)
	mkdir(t, f.outDir)
	return f
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bundle は バンドル <name> のオーバーレイ層（.claude/）に <rel> のファイルを置き、その Bundle を返す。
// テストが渡す rel は "rules/x.md" のようにターゲット内の位置で書き、ここで .claude/ を被せる。
func (f fixture) bundle(t *testing.T, name string, files map[string]string) bundle.Bundle {
	t.Helper()
	dir := filepath.Join(f.tplHome, name)
	mkdir(t, dir)
	for rel, content := range files {
		writeFile(t, filepath.Join(dir, ".claude", rel), content)
	}
	return bundle.Bundle{Name: name, Dir: dir}
}

func (f fixture) opts() Options {
	return Options{TplHome: f.tplHome, ArchiveDir: f.archive, ScanLayers: []string{bundle.DefaultOverlay}}
}

func (f fixture) sync(t *testing.T, bundles []bundle.Bundle, o Options) []Action {
	t.Helper()
	acts, err := Sync(f.target, bundles, o)
	if err != nil {
		t.Fatalf("Sync が失敗: %v", err)
	}
	return acts
}

// readlink は symlink のリンク文字列（相対のまま）を返す
func readlink(t *testing.T, p string) string {
	t.Helper()
	target, err := os.Readlink(p)
	if err != nil {
		t.Fatalf("%s は symlink ではありません: %v", p, err)
	}
	return target
}

func kinds(acts []Action, kind string) []string {
	var out []string
	for _, a := range acts {
		if a.Kind == Kind(kind) {
			out = append(out, filepath.Base(a.Path))
		}
	}
	return out
}

// rules は「フラグ 1 個 = ディレクトリ 1 本の symlink」に畳む（.claude/rules/ は再帰探索されるため）。
func TestSync_rulesはフラグ単位のディレクトリsymlink(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "loop", map[string]string{"rules/loop-engineering.md": "規約\n"})

	acts := f.sync(t, []bundle.Bundle{b}, f.opts())

	linkPath := filepath.Join(f.outDir, "rules", "loop")
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf(".claude/rules/loop が作られていない: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink ではない")
	}
	// リンク先の中身が読めること（= リンクが有効）
	if b, err := os.ReadFile(filepath.Join(linkPath, "loop-engineering.md")); err != nil || string(b) != "規約\n" {
		t.Errorf("リンク経由で読めない: %v", err)
	}
	if got := kinds(acts, "created"); len(got) != 1 || got[0] != "loop" {
		t.Errorf("created の報告が不正: %+v", acts)
	}
}

// skills は <name>/SKILL.md が発見規約なので階層を挟めない = エントリ単位のリンク。
func TestSync_skillsはエントリ単位のsymlink(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{
		"skills/wiki-query/SKILL.md":  "q\n",
		"skills/wiki-update/SKILL.md": "u\n",
	})

	f.sync(t, []bundle.Bundle{b}, f.opts())

	for _, name := range []string{"wiki-query", "wiki-update"} {
		p := filepath.Join(f.outDir, "skills", name)
		if _, err := os.Lstat(p); err != nil {
			t.Errorf(".claude/skills/%s が無い: %v", name, err)
		}
		if _, err := os.ReadFile(filepath.Join(p, "SKILL.md")); err != nil {
			t.Errorf("%s のリンクが壊れている: %v", name, err)
		}
	}
	// フラグ名の階層は挟まない
	if _, err := os.Lstat(filepath.Join(f.outDir, "skills", "wiki")); err == nil {
		t.Error("skills はフラグ単位に畳んではいけない")
	}
}

func TestSync_agentsやhooksも同じ規則で配る(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "loop", map[string]string{
		"agents/fixer.md":      "fixer\n",
		"hooks/stop_verify.sh": "#!/bin/bash\n",
		"commands/foo.md":      "foo\n",
	})

	f.sync(t, []bundle.Bundle{b}, f.opts())

	for _, rel := range []string{"agents/fixer.md", "hooks/stop_verify.sh", "commands/foo.md"} {
		if _, err := os.Lstat(filepath.Join(f.outDir, rel)); err != nil {
			t.Errorf(".claude/%s が無い: %v", rel, err)
		}
	}
}

func TestSync_partialsは配らない(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"partials/x.tmpl": "x"})

	f.sync(t, []bundle.Bundle{b}, f.opts())

	if _, err := os.Lstat(filepath.Join(f.outDir, "partials")); err == nil {
		t.Error("partials はテンプレの部品なので配布対象外")
	}
}

// symlink は相対で張る（複数マシンでユーザー名が違っても壊れないため）
func TestSync_symlinkは相対パス(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "loop", map[string]string{"rules/x.md": "x"})

	f.sync(t, []bundle.Bundle{b}, f.opts())

	target := readlink(t, filepath.Join(f.outDir, "rules", "loop"))
	if filepath.IsAbs(target) {
		t.Errorf("絶対パスで張られている: %s", target)
	}
	if !strings.Contains(target, "llm-tpl") {
		t.Errorf("リンク先が不正: %s", target)
	}
}

func TestSync_冪等(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "s"})

	f.sync(t, []bundle.Bundle{b}, f.opts())
	acts := f.sync(t, []bundle.Bundle{b}, f.opts())

	if len(kinds(acts, "created")) != 0 || len(kinds(acts, "removed")) != 0 {
		t.Errorf("2 回目に変更が出ている: %+v", acts)
	}
	if len(kinds(acts, "kept")) != 1 {
		t.Errorf("kept の報告が不正: %+v", acts)
	}
}

// OFF にしたら自分が張ったリンクだけを消す
func TestSync_OFFで所有物だけ消える(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "s"})
	f.sync(t, []bundle.Bundle{b}, f.opts())

	// 自分の所有ではない実体・リンクを置く
	writeFile(t, filepath.Join(f.outDir, "skills", "hand-made", "SKILL.md"), "手書き\n")
	outside := filepath.Join(f.root, "outside")
	mkdir(t, outside)
	if err := os.Symlink(outside, filepath.Join(f.outDir, "skills", "other-link")); err != nil {
		t.Fatal(err)
	}

	acts := f.sync(t, nil, f.opts()) // 全 OFF

	if _, err := os.Lstat(filepath.Join(f.outDir, "skills", "s")); !os.IsNotExist(err) {
		t.Error("OFF なのに所有リンクが残っている")
	}
	if _, err := os.Stat(filepath.Join(f.outDir, "skills", "hand-made", "SKILL.md")); err != nil {
		t.Error("手書きの実体を消してしまった")
	}
	if _, err := os.Lstat(filepath.Join(f.outDir, "skills", "other-link")); err != nil {
		t.Error("バンドル外を指す symlink を消してしまった")
	}
	if got := kinds(acts, "removed"); len(got) != 1 || got[0] != "s" {
		t.Errorf("removed の報告が不正: %+v", acts)
	}
}

// バンドルを削除した後の壊れたリンク（所有物）も掃除する
func TestSync_壊れた所有リンクも消える(t *testing.T) {
	f := newFixture(t)
	skillsDir := filepath.Join(f.outDir, "skills")
	mkdir(t, skillsDir)
	// llm-tpl 配下を指すが実体が無いリンク
	rel, err := filepath.Rel(skillsDir, filepath.Join(f.tplHome, "gone", "skills", "s"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(skillsDir, "s")); err != nil {
		t.Fatal(err)
	}

	f.sync(t, nil, f.opts())

	if _, err := os.Lstat(filepath.Join(skillsDir, "s")); !os.IsNotExist(err) {
		t.Error("壊れた所有リンクが残っている")
	}
}

func TestSync_同内容の実体はsymlinkに置き換える(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "同じ内容\n"})
	// 配布先に原本と同内容の実体（インストーラが書いたが中身は同じ）
	writeFile(t, filepath.Join(f.outDir, "skills", "s", "SKILL.md"), "同じ内容\n")

	acts := f.sync(t, []bundle.Bundle{b}, f.opts())

	if _, err := os.Readlink(filepath.Join(f.outDir, "skills", "s")); err != nil {
		t.Error("symlink に置き換わっていない")
	}
	for _, a := range acts {
		if a.Kind == "archived" {
			t.Errorf("同内容なら退避不要: %+v", a)
		}
	}
}

func TestSync_内容が違う実体は退避してから張る(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "原本\n"})
	writeFile(t, filepath.Join(f.outDir, "skills", "s", "SKILL.md"), "インストーラが更新した内容\n")

	acts := f.sync(t, []bundle.Bundle{b}, f.opts())

	if _, err := os.Readlink(filepath.Join(f.outDir, "skills", "s")); err != nil {
		t.Error("symlink に置き換わっていない")
	}
	var archived string
	for _, a := range acts {
		if a.Kind == "archived" {
			archived = a.Note
		}
	}
	if archived == "" {
		t.Fatal("退避が報告されていない")
	}
	if b, err := os.ReadFile(filepath.Join(archived, "SKILL.md")); err != nil || string(b) != "インストーラが更新した内容\n" {
		t.Errorf("退避内容が不正: %v", err)
	}
}

// DryRun では実体を動かさず、退避先ディレクトリも作らない
func TestSync_DryRunでは退避もしない(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "原本\n"})
	writeFile(t, filepath.Join(f.outDir, "skills", "s", "SKILL.md"), "手書き\n")
	o := f.opts()
	o.DryRun = true

	acts := f.sync(t, []bundle.Bundle{b}, o)

	found := false
	for _, a := range acts {
		if a.Kind == KindArchived {
			found = true
		}
	}
	if !found {
		t.Error("退避予定が報告されていない")
	}
	if _, err := os.Stat(f.archive); err == nil {
		t.Error("DryRun なのに退避先が作られた")
	}
	got, err := os.ReadFile(filepath.Join(f.outDir, "skills", "s", "SKILL.md"))
	if err != nil || string(got) != "手書き\n" {
		t.Errorf("DryRun なのに実体が動いた: %q %v", got, err)
	}
}

// 退避先はターゲット直下なので、ターゲット名は要らない。どのバンドルの何を退避したかだけを名前に残す
func TestSync_退避名にバンドル名が入る(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "原本\n"})
	writeFile(t, filepath.Join(f.outDir, "skills", "s", "SKILL.md"), "手書き\n")

	acts := f.sync(t, []bundle.Bundle{b}, f.opts())

	var archived string
	for _, a := range acts {
		if a.Kind == KindArchived {
			archived = a.Note
		}
	}
	if archived == "" {
		t.Fatal("退避されていない")
	}
	if name := filepath.Base(archived); !strings.HasPrefix(name, "wiki-s.bk.") {
		t.Errorf("退避名にバンドル名が入っていない: %s", name)
	}
}

func TestSync_バンドル間の名前衝突は先勝ちで報告(t *testing.T) {
	f := newFixture(t)
	a := f.bundle(t, "aaa", map[string]string{"skills/dup/SKILL.md": "A\n"})
	z := f.bundle(t, "zzz", map[string]string{"skills/dup/SKILL.md": "Z\n"})

	acts := f.sync(t, []bundle.Bundle{a, z}, f.opts())

	got, err := os.ReadFile(filepath.Join(f.outDir, "skills", "dup", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "A\n" {
		t.Errorf("先勝ちになっていない: %q", got)
	}
	found := false
	for _, act := range acts {
		if act.Kind == "conflict" && strings.Contains(act.Note, "zzz") {
			found = true
		}
	}
	if !found {
		t.Errorf("衝突が報告されていない: %+v", acts)
	}
}

func TestSync_DryRunは書かない(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "s"})
	o := f.opts()
	o.DryRun = true

	acts := f.sync(t, []bundle.Bundle{b}, o)

	if len(kinds(acts, "created")) != 1 {
		t.Errorf("DryRun でも作成予定を報告すべき: %+v", acts)
	}
	if _, err := os.Lstat(filepath.Join(f.outDir, "skills", "s")); !os.IsNotExist(err) {
		t.Error("DryRun なのにリンクが作られた")
	}
}

func TestSync_バンドルにディレクトリが無ければ何もしない(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "commit", map[string]string{"CLAUDE.md.tmpl": "x", "bundle.conf": "y"})

	acts := f.sync(t, []bundle.Bundle{b}, f.opts())

	if len(acts) != 0 {
		t.Errorf("何も起きないべき: %+v", acts)
	}
}

// --- オーバーレイ層の走査（旧実装は深さ 2 固定 + ドット始まりスキップで、ここが全部落ちた）---

// 所有リンクの回収がキルスイッチの土台。ここが壊れると OFF にしてもリンクが外れず、
// 毎回 KindCreated が立って check が恒久的に exit 2 になる（どちらも警告が出ない）。
func TestSync_ドット始まり層の奥にある所有リンクを回収する(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "s", "rules/r.md": "r"})
	f.sync(t, []bundle.Bundle{b}, f.opts())

	// 張れていること（.claude/skills/s と .claude/rules/wiki の 3 階層）
	for _, rel := range []string{"skills/s", "rules/wiki"} {
		if _, err := os.Readlink(filepath.Join(f.outDir, rel)); err != nil {
			t.Fatalf(".claude/%s が張られていない: %v", rel, err)
		}
	}

	// OFF にすると両方外れること
	acts := f.sync(t, nil, f.opts())
	for _, rel := range []string{"skills/s", "rules/wiki"} {
		if _, err := os.Lstat(filepath.Join(f.outDir, rel)); !os.IsNotExist(err) {
			t.Errorf(".claude/%s が外れていない（キルスイッチが死んでいる）", rel)
		}
	}
	if got := kinds(acts, "removed"); len(got) != 2 {
		t.Errorf("removed が 2 件でない: %v", got)
	}
}

// 走査層を定数に閉じている根拠の回帰網。ターゲットがプロジェクトルートへ上がっても、
// ルート直下の無関係な symlink は走査対象にならない。
func TestSync_ターゲットルート直下の無関係なsymlinkは触らない(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "s"})
	f.sync(t, []bundle.Bundle{b}, f.opts())

	// 人がルート直下に張った、棚を指す symlink（＝ 字面判定では「所有」に見えるもの）
	holder := filepath.Join(f.target, "vendor")
	mkdir(t, holder)
	rel, err := filepath.Rel(holder, filepath.Join(f.tplHome, "wiki"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(holder, "shortcut")); err != nil {
		t.Fatal(err)
	}

	f.sync(t, nil, f.opts()) // 全部 OFF
	if _, err := os.Lstat(filepath.Join(holder, "shortcut")); err != nil {
		t.Errorf("走査層の外の symlink を消してしまった: %v", err)
	}
}

// 棚をリポジトリの中に置く構成（記事のデモ・チーム運用）で、棚自身を器と誤認しない。
func TestSync_棚がターゲット内にあっても器と誤認しない(t *testing.T) {
	root := t.TempDir()
	f := fixture{
		root:    root,
		tplHome: filepath.Join(root, "proj", "llm-tpl"), // 棚がターゲットの中
		target:  filepath.Join(root, "proj"),
		outDir:  filepath.Join(root, "proj", ".claude"),
		archive: filepath.Join(root, ".archive"),
	}
	mkdir(t, f.tplHome)
	mkdir(t, f.outDir)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "s"})

	acts := f.sync(t, []bundle.Bundle{b}, f.opts())
	if len(kinds(acts, "removed")) != 0 {
		t.Errorf("棚の中身を所有リンクとして外そうとした: %v", acts)
	}
	if _, err := os.Stat(filepath.Join(f.tplHome, "wiki", ".claude", "skills", "s", "SKILL.md")); err != nil {
		t.Errorf("棚の実体が消えた: %v", err)
	}
}

// 張りたい位置に人が張った symlink があったら、黙って消さずに退避する。
func TestSync_非所有のsymlinkは消さずに退避する(t *testing.T) {
	f := newFixture(t)
	b := f.bundle(t, "wiki", map[string]string{"skills/s/SKILL.md": "原本\n"})

	outside := filepath.Join(f.root, "somewhere")
	writeFile(t, filepath.Join(outside, "SKILL.md"), "人が張った近道\n")
	mkdir(t, filepath.Join(f.outDir, "skills"))
	if err := os.Symlink(outside, filepath.Join(f.outDir, "skills", "s")); err != nil {
		t.Fatal(err)
	}

	acts := f.sync(t, []bundle.Bundle{b}, f.opts())
	if len(kinds(acts, "archived")) == 0 {
		t.Fatalf("退避が報告されていない: %v", acts)
	}
	// 退避物が読めること（消されていない）
	found, err := filepath.Glob(filepath.Join(f.archive, "*"))
	if err != nil || len(found) == 0 {
		t.Fatalf("退避先が空: %v %v", found, err)
	}
	if _, err := os.Readlink(filepath.Join(f.outDir, "skills", "s")); err != nil {
		t.Errorf("張り替えられていない: %v", err)
	}
}

func TestSync_走査層がターゲットの外を指したらエラー(t *testing.T) {
	f := newFixture(t)
	o := f.opts()
	o.ScanLayers = []string{"../escape"}
	if _, err := Sync(f.target, nil, o); err == nil {
		t.Error("ターゲット外を指す走査層が通ってしまった")
	}
}

// 旧レイアウト（層 = "."）でも従来どおり動くこと。移行の途中で退行しない保証。
func TestSync_走査層が空なら旧レイアウトの挙動(t *testing.T) {
	root := t.TempDir()
	f := fixture{
		root:    root,
		tplHome: filepath.Join(root, "llm-tpl"),
		target:  filepath.Join(root, "proj", ".claude"), // 旧: ターゲット = .claude 自身
		outDir:  filepath.Join(root, "proj", ".claude"),
		archive: filepath.Join(root, ".archive"),
	}
	mkdir(t, f.tplHome)
	mkdir(t, f.outDir)

	// 旧レイアウトのバンドル（オーバーレイ層を挟まない）
	dir := filepath.Join(f.tplHome, "wiki")
	writeFile(t, filepath.Join(dir, "skills", "s", "SKILL.md"), "s")
	b := bundle.Bundle{Name: "wiki", Dir: dir}

	o := Options{TplHome: f.tplHome, ArchiveDir: f.archive} // ScanLayers を渡さない
	if _, err := Sync(f.target, []bundle.Bundle{b}, o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(f.outDir, "skills", "s")); err != nil {
		t.Fatalf("旧レイアウトで張れていない: %v", err)
	}
	if _, err := Sync(f.target, nil, o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.outDir, "skills", "s")); !os.IsNotExist(err) {
		t.Error("旧レイアウトで外れていない")
	}
}

// 層を一般化した以上、キルスイッチも .claude 以外で効かなければならない。
// ここが効かないと「.cursor/ に配ったリンクが OFF にしても外れない」になる。
func TestSync_claude以外の層でもOFFで外れる(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.tplHome, "hoge")
	writeFile(t, filepath.Join(dir, ".hogehoge", "test", "aaaa.md"), "A")
	b := bundle.Bundle{Name: "hoge", Dir: dir}

	layers, err := bundle.Layers([]bundle.Bundle{b})
	if err != nil {
		t.Fatal(err)
	}
	o := Options{TplHome: f.tplHome, ArchiveDir: f.archive, ScanLayers: layers}

	if _, err := Sync(f.target, []bundle.Bundle{b}, o); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(f.target, ".hogehoge", "test", "aaaa.md")
	if _, err := os.Readlink(linkPath); err != nil {
		t.Fatalf(".hogehoge へ配れていない: %v", err)
	}

	if _, err := Sync(f.target, nil, o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
		t.Error(".hogehoge のリンクが OFF で外れない（キルスイッチが片肺）")
	}
}
