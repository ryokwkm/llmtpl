package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile はテスト用ファイルを作る（サブディレクトリも作る）
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// parseSets は "name=true" 形式のテスト記述を bool マップへ変換する（テスト専用の糖衣）
func parseSets(t *testing.T, sets []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, s := range sets {
		name, val, ok := strings.Cut(s, "=")
		if !ok {
			t.Fatalf("テストの書き方が不正（name=true|false）: %q", s)
		}
		out[name] = val == "true"
	}
	return out
}

// renderSets は "name=true" 形式でフラグを渡す薄いラッパ
func renderSets(t *testing.T, tmplPath string, partialDirs []string, header string, sets []string) (Result, error) {
	t.Helper()
	flags := parseSets(t, sets)
	return Render(Options{
		TmplPath:    tmplPath,
		PartialDirs: partialDirs,
		Header:      header,
		Flags:       flags,
	})
}

// body はヘッダ無しでレンダリングして本文だけ返す
func body(t *testing.T, tmplPath string, partialDirs []string, sets []string) string {
	t.Helper()
	res, err := renderSets(t, tmplPath, partialDirs, "", sets)
	if err != nil {
		t.Fatalf("Render が失敗: %v", err)
	}
	return string(res.Content)
}

func TestIf_ONで含まれOFFで消える(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "line A\n{{- if .wiki}}\nline B\n{{- end}}\nline C\n")

	if got := body(t, tmpl, nil, []string{"wiki=true"}); got != "line A\nline B\nline C\n" {
		t.Errorf("ON の出力が不正: %q", got)
	}
	if got := body(t, tmpl, nil, []string{"wiki=false"}); got != "line A\nline C\n" {
		t.Errorf("OFF の出力が不正（空行が残っていないか）: %q", got)
	}
}

func TestElse分岐(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "{{if .wiki}}on{{else}}off{{end}}\n")

	if got := body(t, tmpl, nil, []string{"wiki=false"}); got != "off\n" {
		t.Errorf("else 分岐が不正: %q", got)
	}
}

func TestネストIfはANDになる(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "{{if .a}}{{if .b}}both{{end}}{{end}}\n")

	if got := body(t, tmpl, nil, []string{"a=true", "b=true"}); got != "both\n" {
		t.Errorf("a∧b true の出力が不正: %q", got)
	}
	if got := body(t, tmpl, nil, []string{"a=true", "b=false"}); got != "\n" {
		t.Errorf("b=false で出力されてしまう: %q", got)
	}
}

func Test未定義フラグ参照はエラー(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "{{if .typo}}x{{end}}\n")

	if _, err := renderSets(t, tmpl, nil, "", []string{"wiki=true"}); err == nil {
		t.Error("未定義フラグ .typo の参照がエラーになりません（missingkey=error が効いていない）")
	}
}

func Test閉じ忘れはエラー(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "{{if .wiki}}閉じない\n")

	if _, err := renderSets(t, tmpl, nil, "", []string{"wiki=true"}); err == nil {
		t.Error("{{end}} 欠落がエラーになりません")
	}
}

func TestTmplPath未指定はエラー(t *testing.T) {
	if _, err := Render(Options{}); err == nil {
		t.Error("TmplPath 未指定がエラーになりません")
	}
}

// 実運用（CLAUDE.md.tmpl + memory-vault.tmpl）と同じ whitespace イディオムの回帰テスト
func Test部品テンプレ内のifとwhitespaceイディオム(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "partials/vault.tmpl", "{{- if .wiki}}\n- vault の行\n{{- end -}}\n")
	tmpl := writeFile(t, dir, "a.tmpl", "- 前の行\n{{- template \"vault.tmpl\" .}}\n- 後の行\n")
	partials := []string{filepath.Join(dir, "partials")}

	if got := body(t, tmpl, partials, []string{"wiki=true"}); got != "- 前の行\n- vault の行\n- 後の行\n" {
		t.Errorf("ON: 部品挿入の whitespace が不正: %q", got)
	}
	if got := body(t, tmpl, partials, []string{"wiki=false"}); got != "- 前の行\n- 後の行\n" {
		t.Errorf("OFF: 空行や残骸が残っています: %q", got)
	}
}

func Test同名部品は後勝ちでスコープ側が優先(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shared/x.tmpl", "共有版")
	writeFile(t, dir, "scoped/x.tmpl", "スコープ版")
	tmpl := writeFile(t, dir, "a.tmpl", `{{template "x.tmpl" .}}`)

	got := body(t, tmpl, []string{filepath.Join(dir, "shared"), filepath.Join(dir, "scoped")}, nil)
	if got != "スコープ版" {
		t.Errorf("後勝ちになっていません: %q", got)
	}
}

func Test存在しないpartialsディレクトリは無視(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "x\n")

	if got := body(t, tmpl, []string{filepath.Join(dir, "no-such-dir")}, nil); got != "x\n" {
		t.Errorf("出力が不正: %q", got)
	}
}

// 多バイト文字が verbatim に通過することの回帰ガード
// （過去に bash 3.2 の多バイトパースバグを踏んだ経緯があるため明示的に固定する）
func Test多バイト本文はそのまま通過(t *testing.T) {
	dir := t.TempDir()
	src := "## メモリ管理（①②③・「蒸留済み」→ `wiki-update`）🔥⚠️\n"
	tmpl := writeFile(t, dir, "a.tmpl", src)

	if got := body(t, tmpl, nil, nil); got != src {
		t.Errorf("多バイト本文が変化: %q", got)
	}
}

func Testヘッダは指定時だけ先頭1行に入る(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "x\n")
	header := "<!-- GENERATED — do not edit directly. Source: a.tmpl -->"

	res, err := renderSets(t, tmpl, nil, header, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Content) != header+"\nx\n" {
		t.Errorf("ヘッダ付きの出力が不正: %q", res.Content)
	}

	// Header 空 = ヘッダを付けない（コメントを書けない JSON 断片用）
	res, err = renderSets(t, tmpl, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Content) != "x\n" {
		t.Errorf("ヘッダ無しの出力が不正: %q", res.Content)
	}
}

func Testテンプレコメントは出力されない(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "{{/* 人間向けメモ */}}x\n")

	if got := body(t, tmpl, nil, nil); got != "x\n" {
		t.Errorf("テンプレコメントが漏れています: %q", got)
	}
}

// slot: 渡された内容を verbatim に差し込む。改行の作法（先頭 \n・末尾改行なし）は
// 呼び出し側（バンドル合成）の責務で、render は文字列をそのまま置くだけ。
func TestSlotは内容をverbatimに差し込む(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "- 前の行\n{{- slot \"memory\"}}\n- 後の行\n")

	res, err := Render(Options{
		TmplPath: tmpl,
		Flags:    map[string]bool{},
		Slots:    map[string]string{"memory": "\n- 差し込まれた行"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Content) != "- 前の行\n- 差し込まれた行\n- 後の行\n" {
		t.Errorf("slot 差し込みが不正: %q", res.Content)
	}
}

// 消費側に {{slot}} があってもバンドル側の内容が無い（= ON のフラグが誰も寄稿しない）のは正常。
// 空文字を置き、whitespace イディオムどおり空行も残さない。
func TestSlotの内容が無ければ空になる(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "- 前の行\n{{- slot \"memory\"}}\n- 後の行\n")

	res, err := Render(Options{TmplPath: tmpl, Flags: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Content) != "- 前の行\n- 後の行\n" {
		t.Errorf("空 slot で余分な行が残っています: %q", res.Content)
	}
}

// 使われた slot 名を返す（上位が「宣言されたのに消費側に受け口が無い断片」を検出するため）
func TestUsedSlotsに消費したslot名が記録される(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "{{slot \"memory\"}}{{slot \"tail\"}}{{slot \"memory\"}}")

	res, err := Render(Options{
		TmplPath: tmpl,
		Flags:    map[string]bool{},
		Slots:    map[string]string{"memory": "M"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Content) != "MM" {
		t.Errorf("出力が不正: %q", res.Content)
	}
	if len(res.UsedSlots) != 2 || !res.UsedSlots["memory"] || !res.UsedSlots["tail"] {
		t.Errorf("UsedSlots が不正: %v", res.UsedSlots)
	}
}

func TestSlot名が空ならエラー(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", `{{slot ""}}`)

	if _, err := Render(Options{TmplPath: tmpl, Flags: map[string]bool{}}); err == nil {
		t.Error("空の slot 名がエラーになりません")
	}
}

// フラグが空マップでも {{if}} を使わないテンプレは通る（バンドル 0 個の初期状態）
func Testフラグ未指定でも本文だけなら通る(t *testing.T) {
	dir := t.TempDir()
	tmpl := writeFile(t, dir, "a.tmpl", "本文だけ\n")

	res, err := Render(Options{TmplPath: tmpl})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Content) != "本文だけ\n" {
		t.Errorf("出力が不正: %q", res.Content)
	}
}

// Source: ファイルではなく文字列をテンプレとして評価する（frontmatter を剥がした断片用）。
// TmplPath は読み込まれず、名前とエラー表示にだけ使われる。
func TestSourceを指定するとファイルを読まない(t *testing.T) {
	dir := t.TempDir()
	notExist := filepath.Join(dir, "fragment.tmpl") // 実ファイルは作らない

	res, err := Render(Options{
		TmplPath: notExist,
		Source:   []byte("{{if .wiki}}ON{{else}}OFF{{end}}\n"),
		Flags:    map[string]bool{"wiki": true},
	})
	if err != nil {
		t.Fatalf("Source のレンダリングが失敗: %v", err)
	}
	if string(res.Content) != "ON\n" {
		t.Errorf("出力が不正: %q", res.Content)
	}
}

func TestSourceの文法エラーはパスを添えて返る(t *testing.T) {
	dir := t.TempDir()
	_, err := Render(Options{
		TmplPath: filepath.Join(dir, "bad.tmpl"),
		Source:   []byte("{{if .wiki}}閉じない"),
		Flags:    map[string]bool{"wiki": true},
	})
	if err == nil {
		t.Fatal("文法エラーがエラーになりません")
	}
	if !strings.Contains(err.Error(), "bad.tmpl") {
		t.Errorf("どの断片かがメッセージに無い: %v", err)
	}
}
