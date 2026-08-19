package fileout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const marker = "<!-- GENERATED"

func opts(archiveDir string) Options {
	return Options{ArchiveDir: archiveDir, GeneratedMarker: marker}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 一時ファイル（*.tmp.<pid>）が残っていないことを確認する
func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("一時ファイルが残っています: %s", e.Name())
		}
	}
}

func TestWrite_新規作成と親ディレクトリ作成(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "nested/deep/out.md")

	res, err := Write(dest, []byte("body\n"), opts(filepath.Join(dir, ".archive")))
	if err != nil {
		t.Fatalf("Write が失敗: %v", err)
	}
	if !res.Changed {
		t.Error("新規作成は Changed=true であるべき")
	}
	if res.Archived != "" {
		t.Errorf("退避は不要: %q", res.Archived)
	}
	if got := read(t, dest); got != "body\n" {
		t.Errorf("内容が不正: %q", got)
	}
	assertNoTemp(t, filepath.Dir(dest))
}

func TestWrite_同内容ならChangedはfalse(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.md")
	content := []byte(marker + " -->\nbody\n")

	if _, err := Write(dest, content, opts(filepath.Join(dir, ".archive"))); err != nil {
		t.Fatal(err)
	}
	res, err := Write(dest, content, opts(filepath.Join(dir, ".archive")))
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("同内容の再書き込みは Changed=false であるべき（冪等）")
	}
	if got := read(t, dest); string(content) != got {
		t.Errorf("内容が変化: %q", got)
	}
}

func TestWrite_生成物は差分ありでも退避せず上書き(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.md")
	archive := filepath.Join(dir, ".archive")
	if err := os.WriteFile(dest, []byte(marker+" 前回 -->\n古い本文\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Write(dest, []byte(marker+" 今回 -->\n新しい本文\n"), opts(archive))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Error("内容が変わったので Changed=true であるべき")
	}
	if res.Archived != "" {
		t.Errorf("生成物の上書きで退避してはいけない: %q", res.Archived)
	}
	if !strings.Contains(read(t, dest), "新しい本文") {
		t.Error("上書きされていない")
	}
	if _, err := os.Stat(archive); err == nil {
		t.Error(".archive が作られてしまった")
	}
}

func TestWrite_手書き実体は退避してから上書き(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.md")
	archive := filepath.Join(dir, ".archive")
	if err := os.WriteFile(dest, []byte("手で書いた本文\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Write(dest, []byte(marker+" -->\n生成した本文\n"), opts(archive))
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived == "" {
		t.Fatal("手書き実体が退避されていない")
	}
	if got := read(t, res.Archived); got != "手で書いた本文\n" {
		t.Errorf("退避内容が不正: %q", got)
	}
	if !strings.Contains(read(t, dest), "生成した本文") {
		t.Error("上書きされていない")
	}
	if !strings.HasPrefix(filepath.Base(res.Archived), "out.md.bk.") {
		t.Errorf("退避ファイル名が不正: %s", res.Archived)
	}
}

func TestWrite_手書き実体でも同内容なら退避しない(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.md")
	archive := filepath.Join(dir, ".archive")
	content := []byte("ヘッダ無しだが同内容\n")
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Write(dest, content, opts(archive))
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived != "" {
		t.Errorf("同内容なら退避不要: %q", res.Archived)
	}
	if res.Changed {
		t.Error("同内容なら Changed=false")
	}
}

func TestWrite_DryRunは書き込まない(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.md")
	o := opts(filepath.Join(dir, ".archive"))
	o.DryRun = true

	res, err := Write(dest, []byte("新規\n"), o)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Error("DryRun でも差分は Changed=true で報告すべき")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("DryRun なのにファイルが作られた")
	}
}

func TestWrite_DryRunは退避もしない(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.md")
	archive := filepath.Join(dir, ".archive")
	if err := os.WriteFile(dest, []byte("手書き\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := opts(archive)
	o.DryRun = true

	res, err := Write(dest, []byte("生成\n"), o)
	if err != nil {
		t.Fatal(err)
	}
	if !res.WouldArchive {
		t.Error("DryRun では WouldArchive で退避予定を知らせるべき")
	}
	if _, err := os.Stat(archive); err == nil {
		t.Error("DryRun なのに .archive が作られた")
	}
	if got := read(t, dest); got != "手書き\n" {
		t.Errorf("DryRun なのに書き換わった: %q", got)
	}
}

func TestWrite_失敗しても既存を壊さない(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.md")
	if err := os.WriteFile(dest, []byte(marker+" -->\n既存\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 退避先をファイルにしておくと MkdirAll が失敗する（退避が必要なケースを作る）
	archive := filepath.Join(dir, "archive-as-file")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("手書き\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Write(dest, []byte("新しい\n"), opts(archive)); err == nil {
		t.Fatal("退避に失敗したのにエラーになっていない")
	}
	if got := read(t, dest); got != "手書き\n" {
		t.Errorf("失敗時に既存が壊れた: %q", got)
	}
	assertNoTemp(t, dir)
}

func TestIsGenerated(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"1行目にマーカ", marker + " 原本: x -->\n本文\n", true},
		{"本文にマーカ（1行目でない）", "本文\n" + marker + " -->\n", false},
		{"マーカなし", "本文だけ\n", false},
		{"改行なしでマーカのみ", marker + " -->", true},
		{"空", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isGenerated([]byte(c.content), marker); got != c.want {
				t.Errorf("IsGenerated=%v want %v", got, c.want)
			}
		})
	}
}

// 退避先は全ターゲット共通になりうる。タイムスタンプは秒精度なので、
// 同じ label で同一秒に 2 回退避しても**先の退避を上書きしない**こと（手編集の消失防止）。
func TestArchivePath_同一秒でも衝突しない(t *testing.T) {
	dir := t.TempDir()

	first, err := ArchivePath(dir, "settings.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := ArchivePath(dir, "settings.json")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("同じパスを返した: %s", second)
	}
}

func TestWrite_同名の退避が2回起きても両方残る(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, ".archive")

	// 別のターゲットの同名ファイル（basename が同じ）を続けて退避する
	for i, body := range []string{"A の手書き\n", "B の手書き\n"} {
		dest := filepath.Join(dir, fmt.Sprintf("target%d", i), "settings.json")
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Write(dest, []byte("生成物\n"), Options{ArchiveDir: archive, ArchiveLabel: "settings.json"}); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("退避が %d 件（2 件残るべき）: %v", len(entries), entries)
	}
	var bodies []string
	for _, e := range entries {
		bodies = append(bodies, read(t, filepath.Join(archive, e.Name())))
	}
	if bodies[0] == bodies[1] {
		t.Errorf("退避内容が同じ = 片方が失われている: %v", bodies)
	}
}

func TestHash(t *testing.T) {
	a := Hash([]byte("x"))
	if a != Hash([]byte("x")) {
		t.Error("同内容で異なるハッシュ")
	}
	if a == Hash([]byte("y")) {
		t.Error("異なる内容で同じハッシュ")
	}
	if len(a) != 64 {
		t.Errorf("sha256 16 進の長さが不正: %d", len(a))
	}
}

// JSON など先頭にマーカを書けない生成物は、前回の内容ハッシュで「自分が書いたもの」と判定する。
func TestWrite_KnownHash一致なら退避しない(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "settings.json")
	archive := filepath.Join(dir, ".archive")
	prev := []byte("{\"a\":1}\n")
	if err := os.WriteFile(dest, prev, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Write(dest, []byte("{\"a\":2}\n"), Options{ArchiveDir: archive, KnownHash: Hash(prev)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived != "" {
		t.Errorf("前回の生成物なので退避不要: %q", res.Archived)
	}
	if !res.Changed || read(t, dest) != "{\"a\":2}\n" {
		t.Errorf("上書きされていない: %q", read(t, dest))
	}
}

func TestWrite_KnownHash不一致なら退避する(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "settings.json")
	archive := filepath.Join(dir, ".archive")
	if err := os.WriteFile(dest, []byte("{\"外部が書いた\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Write(dest, []byte("{\"a\":2}\n"), Options{
		ArchiveDir: archive,
		KnownHash:  Hash([]byte("別の内容")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived == "" {
		t.Error("外部の書き込みは退避すべき")
	}
}

// マーカもハッシュも無い（初回・state 消失）場合は安全側 = 差分があれば退避。
func TestWrite_マーカ未指定なら常に退避扱い(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "settings.json")
	archive := filepath.Join(dir, ".archive")
	if err := os.WriteFile(dest, []byte("{\"a\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Write(dest, []byte("{\"a\":2}\n"), Options{ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived == "" {
		t.Error("マーカ未指定では差分ありの既存を退避すべき")
	}
}
