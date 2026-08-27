package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryokwkm/llmtpl/internal/apply"
	"github.com/ryokwkm/llmtpl/internal/msg"
)

// shelf はバンドルルートを 1 つ組む（bundle 1 個 + CLAUDE.md 断片）。
func shelf(t *testing.T, dir, bundleName, fragment string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, bundleName, "bundle.conf"), "description: テスト\n")
	writeFile(t, filepath.Join(dir, bundleName, "CLAUDE.md.tmpl"), fragment)
}

// 環境由来のルート解決（LLMTPL_HOME / XDG）をテストから切り離す。
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv(apply.EnvHome, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// bundle_root はそれを書いた conf のターゲットだけに効く。
// 別々の値を書いた 2 ターゲットが、それぞれ自分の棚から合成されること（かつては競合エラー）。
func TestApply_bundle_rootはターゲットごとに効く(t *testing.T) {
	isolateHome(t)
	base := t.TempDir()
	shelf(t, filepath.Join(base, "shelfX"), "wiki", "\n## wikiX\n")
	shelf(t, filepath.Join(base, "shelfY"), "loop", "\n## loopY\n")

	proj := filepath.Join(base, "proj")
	writeFile(t, filepath.Join(proj, "a", "llmtpl.conf"), "bundle_root = ../../shelfX\nwiki = true\n")
	writeFile(t, filepath.Join(proj, "a", "CLAUDE.md.tmpl"), "# A\n")
	writeFile(t, filepath.Join(proj, "b", "llmtpl.conf"), "bundle_root = ../../shelfY\nloop = true\n")
	writeFile(t, filepath.Join(proj, "b", "CLAUDE.md.tmpl"), "# B\n")
	t.Chdir(proj)

	out := captureStdout(t, func() {
		if err := runApply(nil, &commonFlags{}, false); err != nil {
			t.Fatalf("runApply: %v", err)
		}
	})

	if got := readFileOrFail(t, filepath.Join(proj, "a", "CLAUDE.md")); !strings.Contains(got, "wikiX") {
		t.Errorf("a が自分の棚から合成されていない:\n%s", got)
	}
	if got := readFileOrFail(t, filepath.Join(proj, "b", "CLAUDE.md")); !strings.Contains(got, "loopY") {
		t.Errorf("b が自分の棚から合成されていない:\n%s", got)
	}
	// どの棚で合成したかが読めるよう、ルートの行はルートごとに出る
	if !strings.Contains(out, "shelfX") || !strings.Contains(out, "shelfY") {
		t.Errorf("バンドルルートの行がルートごとに出ていない:\n%s", out)
	}
}

// 親探索の起点は cwd ではなく各ターゲットのディレクトリ。
// grp1/a は grp1/llm-tpl を、grp2/b は grp2/llm-tpl を見つけること
// （cwd 起点だと base に llm-tpl が無く、どちらも解決できない）。
func TestApply_親探索は各ターゲットを起点に辿る(t *testing.T) {
	isolateHome(t)
	base := t.TempDir()
	shelf(t, filepath.Join(base, "grp1", "llm-tpl"), "wiki", "\n## wikiA\n")
	writeFile(t, filepath.Join(base, "grp1", "a", "llmtpl.conf"), "wiki = true\n")
	writeFile(t, filepath.Join(base, "grp1", "a", "CLAUDE.md.tmpl"), "# A\n")
	shelf(t, filepath.Join(base, "grp2", "llm-tpl"), "loop", "\n## loopB\n")
	writeFile(t, filepath.Join(base, "grp2", "b", "llmtpl.conf"), "loop = true\n")
	writeFile(t, filepath.Join(base, "grp2", "b", "CLAUDE.md.tmpl"), "# B\n")
	t.Chdir(base)

	if err := runApply(nil, &commonFlags{}, false); err != nil {
		t.Fatalf("runApply: %v", err)
	}

	if got := readFileOrFail(t, filepath.Join(base, "grp1", "a", "CLAUDE.md")); !strings.Contains(got, "wikiA") {
		t.Errorf("a が grp1/llm-tpl から合成されていない:\n%s", got)
	}
	if got := readFileOrFail(t, filepath.Join(base, "grp2", "b", "CLAUDE.md")); !strings.Contains(got, "loopB") {
		t.Errorf("b が grp2/llm-tpl から合成されていない:\n%s", got)
	}
}

// status はルートごとに「バンドルルート:」の行と表を分ける。
func TestStatus_ルートごとに表を分ける(t *testing.T) {
	isolateHome(t)
	base := t.TempDir()
	shelf(t, filepath.Join(base, "shelfX"), "wiki", "\n## wikiX\n")
	shelf(t, filepath.Join(base, "shelfY"), "loop", "\n## loopY\n")
	proj := filepath.Join(base, "proj")
	writeFile(t, filepath.Join(proj, "a", "llmtpl.conf"), "bundle_root = ../../shelfX\nwiki = true\n")
	writeFile(t, filepath.Join(proj, "a", "CLAUDE.md.tmpl"), "# A\n")
	writeFile(t, filepath.Join(proj, "b", "llmtpl.conf"), "bundle_root = ../../shelfY\nloop = true\n")
	writeFile(t, filepath.Join(proj, "b", "CLAUDE.md.tmpl"), "# B\n")
	t.Chdir(proj)

	out := captureStdout(t, func() {
		rs, err := resolveScope(nil, &commonFlags{})
		if err != nil {
			t.Fatalf("resolveScope: %v", err)
		}
		if err := printStatus(rs); err != nil {
			t.Fatalf("printStatus: %v", err)
		}
	})

	if !strings.Contains(out, "shelfX") || !strings.Contains(out, "shelfY") {
		t.Errorf("ルートごとの見出しが無い:\n%s", out)
	}
	// 列（フラグ名）はそれぞれの棚のものだけが出る
	if !strings.Contains(out, "wiki") || !strings.Contains(out, "loop") {
		t.Errorf("各棚のフラグ列が無い:\n%s", out)
	}
}

// 同じ棚を指すターゲットが複数あっても LoadRoot は 1 回で、表示は従来と同じ形のまま。
func TestApply_単一ルートなら表示は従来どおり(t *testing.T) {
	isolateHome(t)
	base := t.TempDir()
	shelf(t, filepath.Join(base, "llm-tpl"), "wiki", "\n## wikiZ\n")
	writeFile(t, filepath.Join(base, "a", "llmtpl.conf"), "wiki = true\n")
	writeFile(t, filepath.Join(base, "a", "CLAUDE.md.tmpl"), "# A\n")
	writeFile(t, filepath.Join(base, "b", "llmtpl.conf"), "wiki = true\n")
	writeFile(t, filepath.Join(base, "b", "CLAUDE.md.tmpl"), "# B\n")
	t.Chdir(base)

	out := captureStdout(t, func() {
		if err := runApply(nil, &commonFlags{}, false); err != nil {
			t.Fatalf("runApply: %v", err)
		}
	})

	prefix := strings.Split(msg.M.Cmd.BundleRootLine, "%s")[0]
	if got := strings.Count(out, prefix); got != 1 {
		t.Errorf("単一ルートでルートの行が %d 回出ている（1 回のはず）:\n%s", got, out)
	}
}
