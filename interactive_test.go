package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/ryokwkm/llmtpl/internal/apply"
	"github.com/ryokwkm/llmtpl/internal/confedit"
	"github.com/ryokwkm/llmtpl/internal/flags"
	"github.com/ryokwkm/llmtpl/internal/msg"
)

// go test は非 TTY で走るので、このテスト自体が「対話モードへ入らない」ことの検証になる。
// ここが壊れると CI とパイプ越しの呼び出しがフォームで固まる。
func Test引数なしは非TTYならhelpを出す(t *testing.T) {
	if isInteractive() {
		t.Skip("端末から実行されている（このテストは非 TTY を前提にする）")
	}

	var out strings.Builder
	cmd := newRootCmd()
	cmd.SetArgs(nil)
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("引数なしはエラーにならないはず: %v", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("help が出ていない:\n%s", out.String())
	}
}

func Test中止はexit130(t *testing.T) {
	if got := exitCode(canceledErr{}); got != 130 {
		t.Errorf("対話の中止は exit 130: %d", got)
	}
}

func TestOptionLabel(t *testing.T) {
	cases := []struct {
		name string
		it   item
		want string
	}{
		{
			name: "説明があれば添える",
			it:   item{Name: "wiki", Desc: "Obsidian wiki 一式"},
			want: "wiki — Obsidian wiki 一式",
		},
		{
			name: "説明が無ければ名前だけ",
			it:   item{Name: "wiki"},
			want: "wiki",
		},
		{
			// 外すと `= false` の明示追記が起きるので、選ぶ前に既定 ON だと分かる必要がある
			name: "既定 ON は注記する",
			it:   item{Name: "commit", Desc: "コミット方針", DefaultOn: true},
			want: "commit — コミット方針 " + msg.M.Interactive.DefaultOnNote,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := optionLabel(tc.it, 80); got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// writtenFlags が実効値ではなく「conf に行があるもの」だけを返すことを固定する。
// ここが実効値に化けると、既定値で足りるフラグまで conf へ書き込まれる（差分最小が壊れる）。
func TestWrittenFlags_行のあるものだけ返す(t *testing.T) {
	got := writtenFlags([]item{
		{Name: "commit", Written: true, Current: true, Effective: true},
		{Name: "wiki", Written: false, Effective: true, DefaultOn: true}, // 既定で ON なだけ
		{Name: "loop", Written: true, Current: false, Effective: false},
	})
	want := flags.Set{"commit": true, "loop": false}
	if len(got) != len(want) {
		t.Fatalf("件数 = %d, want %d (%v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestBundleItems_実効値と説明を組み立てる(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "commit", "bundle.conf"), "description: コミット方針\n")
	writeFile(t, filepath.Join(home, "wiki", "CLAUDE.md.tmpl"), "wiki\n") // bundle.conf 無し
	writeFile(t, filepath.Join(home, apply.DefaultsConfName), "commit = true\n")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, apply.TargetConfName), "wiki = true\n")

	root, err := apply.LoadRoot(home)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	items, err := bundleItems(root, apply.Target{Dir: dir})
	if err != nil {
		t.Fatalf("bundleItems: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("件数 = %d, want 2", len(items))
	}
	// 名前順
	if items[0].Name != "commit" || items[1].Name != "wiki" {
		t.Fatalf("名前順でない: %s, %s", items[0].Name, items[1].Name)
	}
	// defaults 由来の ON は「実効値 ON・行は無い」
	if !items[0].Effective || items[0].Written || !items[0].DefaultOn {
		t.Errorf("commit = %+v", items[0])
	}
	if items[0].Desc != "コミット方針" {
		t.Errorf("説明が読めていない: %q", items[0].Desc)
	}
	// conf 由来の ON は「実効値 ON・行あり」。bundle.conf が無い側は説明が空
	if !items[1].Effective || !items[1].Written || !items[1].Current || items[1].DefaultOn {
		t.Errorf("wiki = %+v", items[1])
	}
	if items[1].Desc != "" {
		t.Errorf("説明は空のはず: %q", items[1].Desc)
	}
}

// --- フロー全体 --------------------------------------------------------------
// フォームは PTY を要求するので、prompter を差し替えて「conf をどう書き換え、何を apply したか」
// を確かめる。ここが無いと対話モードの自動検証は optionLabel 止まりになる。

// flowFixture はバンドルルート + ターゲットを組み、cwd をターゲットへ移す。
type flowFixture struct {
	dir      string // ターゲット
	confPath string
}

func newFlowFixture(t *testing.T, conf string) flowFixture {
	t.Helper()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "commit", "bundle.conf"), "description: コミット方針\n")
	writeFile(t, filepath.Join(home, "commit", "CLAUDE.md.tmpl"), "\n## コミット方針\n")
	writeFile(t, filepath.Join(home, "wiki", "bundle.conf"), "description: wiki 一式\n")
	writeFile(t, filepath.Join(home, "wiki", "CLAUDE.md.tmpl"), "\n## wiki\n")
	writeFile(t, filepath.Join(home, "loop", "bundle.conf"), "description: ループ規約\n")
	writeFile(t, filepath.Join(home, "loop", "CLAUDE.md.tmpl"), "\n## ループ\n")
	writeFile(t, filepath.Join(home, apply.DefaultsConfName), "commit = true\n")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md.tmpl"),
		"# テスト\n{{- slot \"commit\"}}\n{{- slot \"wiki\"}}\n{{- slot \"loop\"}}\n")
	confPath := filepath.Join(dir, apply.TargetConfName)
	if conf != "" {
		writeFile(t, confPath, conf)
	}

	t.Setenv(apply.EnvHome, home)
	t.Chdir(dir)
	return flowFixture{dir: dir, confPath: confPath}
}

// stubPrompter は「そのフラグ集合を選んで、すべて はい と答える」尋ね方。
func stubPrompter(picked ...string) prompter {
	return prompter{
		AskCreate: func(string, int) (bool, error) { return true, nil },
		AskFlags:  func([]item) ([]string, error) { return picked, nil },
		AskApply:  func() (bool, error) { return true, nil },
	}
}

func TestInteractiveFlow_選択をconfへ書いてapplyまで走る(t *testing.T) {
	// commit は defaults で ON・conf に行なし / wiki は conf で ON
	fx := newFlowFixture(t, "# 手で書いたコメント\nwiki = true\n")

	// wiki を外して loop を入れる（commit は ON のまま = 既定で足りるので書かれないはず）
	if err := interactiveFlow(stubPrompter("commit", "loop")); err != nil {
		t.Fatalf("interactiveFlow: %v", err)
	}

	got := readFileOrFail(t, fx.confPath)
	want := "# 手で書いたコメント\nwiki = false\nloop = true\n"
	if got != want {
		t.Errorf("conf\n got: %q\nwant: %q", got, want)
	}

	// apply まで走って CLAUDE.md が ON のバンドルだけで組まれている
	md := readFileOrFail(t, filepath.Join(fx.dir, "CLAUDE.md"))
	if !strings.Contains(md, "## コミット方針") || !strings.Contains(md, "## ループ") {
		t.Errorf("ON の断片が入っていない:\n%s", md)
	}
	if strings.Contains(md, "## wiki") {
		t.Errorf("OFF にした断片が残っている:\n%s", md)
	}
}

func TestInteractiveFlow_確認でいいえならconfもapplyも走らない(t *testing.T) {
	src := "wiki = true\n"
	fx := newFlowFixture(t, src)

	p := stubPrompter("commit")
	p.AskApply = func() (bool, error) { return false, nil }

	err := interactiveFlow(p)
	if _, ok := err.(canceledErr); !ok {
		t.Fatalf("中止として返るべき: %v", err)
	}
	if got := readFileOrFail(t, fx.confPath); got != src {
		t.Errorf("conf が書き換わっている: %q", got)
	}
	if fileExists(filepath.Join(fx.dir, "CLAUDE.md")) {
		t.Error("apply が走ってしまっている")
	}
}

func TestInteractiveFlow_選択で中止すればconfを触らない(t *testing.T) {
	src := "wiki = true\n"
	fx := newFlowFixture(t, src)

	p := stubPrompter()
	p.AskFlags = func([]item) ([]string, error) { return nil, huh.ErrUserAborted }

	err := interactiveFlow(p)
	if _, ok := err.(canceledErr); !ok {
		t.Fatalf("huh の中止は canceledErr へ変換されるべき: %v", err)
	}
	if got := readFileOrFail(t, fx.confPath); got != src {
		t.Errorf("conf が書き換わっている: %q", got)
	}
}

func TestInteractiveFlow_confが無ければ作成を尋ねる(t *testing.T) {
	t.Run("はいなら作って続ける", func(t *testing.T) {
		fx := newFlowFixture(t, "")

		asked := false
		p := stubPrompter("wiki")
		p.AskCreate = func(string, int) (bool, error) { asked = true; return true, nil }

		if err := interactiveFlow(p); err != nil {
			t.Fatalf("interactiveFlow: %v", err)
		}
		if !asked {
			t.Error("作成を尋ねていない")
		}
		// commit は defaults ON なので、外すと明示 false が要る
		want := "commit = false\nwiki = true\n"
		if got := readFileOrFail(t, fx.confPath); got != want {
			t.Errorf("conf\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("いいえなら何も作らずexit0", func(t *testing.T) {
		fx := newFlowFixture(t, "")

		p := stubPrompter()
		p.AskCreate = func(string, int) (bool, error) { return false, nil }
		p.AskFlags = func([]item) ([]string, error) {
			t.Error("断ったのに選択画面まで進んでいる")
			return nil, nil
		}

		if err := interactiveFlow(p); err != nil {
			t.Fatalf("断るのはエラーではない: %v", err)
		}
		if fileExists(fx.confPath) {
			t.Error("断ったのに conf が作られている")
		}
	})

	// 全フラグが既定のままだと変更ゼロになるが、目印としての conf は残す必要がある
	t.Run("変更ゼロでも空のconfを作る", func(t *testing.T) {
		fx := newFlowFixture(t, "")

		if err := interactiveFlow(stubPrompter("commit")); err != nil {
			t.Fatalf("interactiveFlow: %v", err)
		}
		if !fileExists(fx.confPath) {
			t.Fatal("conf が作られていない")
		}
		if got := readFileOrFail(t, fx.confPath); got != "" {
			t.Errorf("空で作るべき: %q", got)
		}
	})
}

// 他ターゲットの件数は cwd を引いて数える。len(targets)-1 だと、conf を今から作る経路
// （cwd がまだ targets に入っていない）で 1 件少なく出る。
func TestInteractiveFlow_他ターゲットの件数(t *testing.T) {
	cases := []struct {
		name string
		conf string // cwd の llmtpl.conf（"" なら新規作成の経路）
		want string
	}{
		{name: "conf が既にある", conf: "wiki = true\n", want: "2"},
		{name: "conf をこれから作る", conf: "", want: "2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFlowFixture(t, tc.conf)
			// 配下に 2 件のターゲットを置く
			writeFile(t, filepath.Join(fx.dir, "a", apply.TargetConfName), "wiki = true\n")
			writeFile(t, filepath.Join(fx.dir, "b", apply.TargetConfName), "wiki = true\n")

			out := captureStdout(t, func() {
				if err := interactiveFlow(stubPrompter("commit")); err != nil {
					t.Fatalf("interactiveFlow: %v", err)
				}
			})
			want := fmt.Sprintf(msg.M.Interactive.OtherTargetsHint, 2)
			if !strings.Contains(out, strings.TrimSpace(want)) {
				t.Errorf("他ターゲットの件数が %s 件と出ていない:\n%s", tc.want, out)
			}
		})
	}
}

// captureStdout は f の間の標準出力を集める（表示件数の検証に要る）。
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var b strings.Builder
		io.Copy(&b, r)
		done <- b.String()
	}()

	f()

	os.Stdout = orig
	w.Close()
	out := <-done
	r.Close()
	return out
}

func TestWriteConf(t *testing.T) {
	t.Run("変更を書き込む", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), apply.TargetConfName)
		writeFile(t, path, "# メモ\ncommit = true\n")

		old := true
		if err := writeConf(path, "# メモ\ncommit = true\n",
			[]confedit.Change{{Name: "commit", Old: &old, New: false}}, true); err != nil {
			t.Fatalf("writeConf: %v", err)
		}
		if got := readFileOrFail(t, path); got != "# メモ\ncommit = false\n" {
			t.Errorf("内容が違う: %q", got)
		}
	})

	t.Run("変更ゼロで既存があれば触らない", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), apply.TargetConfName)
		src := "# 手で書いたコメント\ncommit = true\n"
		writeFile(t, path, src)

		if err := writeConf(path, src, nil, true); err != nil {
			t.Fatalf("writeConf: %v", err)
		}
		if got := readFileOrFail(t, path); got != src {
			t.Errorf("書き換わっている: %q", got)
		}
	})

	// 「作りますか？→ はい」で全フラグが既定のままだと変更ゼロになる。それでもファイルは
	// 作らないと、llmtpl.conf の存在＝ターゲットの目印が立たず次も同じ質問になる
	t.Run("変更ゼロでもファイルが無ければ作る", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), apply.TargetConfName)

		if err := writeConf(path, "", nil, false); err != nil {
			t.Fatalf("writeConf: %v", err)
		}
		if got := readFileOrFail(t, path); got != "" {
			t.Errorf("空で作るべき: %q", got)
		}
	})
}

func TestReadFile_無ければ空文字(t *testing.T) {
	got, err := readFile(filepath.Join(t.TempDir(), "llmtpl.conf"))
	if err != nil {
		t.Fatalf("無いファイルはエラーにしない: %v", err)
	}
	if got != "" {
		t.Errorf("空文字を返すべき: %q", got)
	}
}

func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// --- 1 行に収める ------------------------------------------------------------
// 説明が長いと端末で折り返す。huh は 1 オプションを 1 行として数えるので viewport の高さが
// 足りなくなり、**先頭のバンドルが画面から消える**（2026-08-26 に実機で agenttrail が消えて発覚）。
// 折り返さないことが表示の前提になる。

func TestOptionLabel_幅に収める(t *testing.T) {
	long := item{Name: "reporter", Desc: "claude-task-reporter（応答完了時の LLM 要約 →デスクトップ通知 + 音声読み上げ）の hook 登録（実体は外部リポジトリ）"}

	for _, w := range []int{20, 40, 72, 100} {
		got := optionLabel(long, w)
		if displayWidth(got) > w {
			t.Errorf("幅 %d に収まっていない（%d）: %q", w, displayWidth(got), got)
		}
		if !strings.HasPrefix(got, "reporter") {
			t.Errorf("フラグ名が消えている: %q", got)
		}
		if strings.Contains(got, "\n") {
			t.Errorf("改行が入っている: %q", got)
		}
	}
}

// フラグ名は削らない。切れると何を選んでいるのか分からなくなる（幅を超えても残す）
func TestOptionLabel_幅が足りなくても名前は残す(t *testing.T) {
	it := item{Name: "progress-digest", Desc: "横断のタスク把握 skill"}
	if got := optionLabel(it, 5); !strings.HasPrefix(got, "progress-digest") {
		t.Errorf("名前が削られている: %q", got)
	}
}

// 既定 ON の注記は説明より優先して残す（外すと `= false` の明示追記が起きるため）
func TestOptionLabel_既定ONの注記は説明より優先(t *testing.T) {
	it := item{Name: "commit", Desc: strings.Repeat("長い説明", 30), DefaultOn: true}
	got := optionLabel(it, 60)
	if displayWidth(got) > 60 {
		t.Errorf("幅に収まっていない（%d）: %q", displayWidth(got), got)
	}
	if !strings.HasSuffix(got, msg.M.Interactive.DefaultOnNote) {
		t.Errorf("既定 ON の注記が落ちている: %q", got)
	}
}

func TestTruncateWidth(t *testing.T) {
	cases := []struct {
		s    string
		max  int
		want string
	}{
		{"abcdef", 10, "abcdef"},
		{"abcdef", 6, "abcdef"},
		{"abcdef", 5, "abcd…"},
		{"あいうえお", 10, "あいうえお"},
		{"あいうえお", 7, "あいう…"}, // 全角 3 つ（6）+ … （1）= 7
		{"あいうえお", 0, ""},
		{"a", 1, "a"},
	}
	for _, c := range cases {
		got := truncateWidth(c.s, c.max)
		if got != c.want {
			t.Errorf("truncateWidth(%q, %d) = %q, want %q", c.s, c.max, got, c.want)
		}
		if displayWidth(got) > c.max {
			t.Errorf("truncateWidth(%q, %d) = %q が幅 %d を超えている（%d）",
				c.s, c.max, got, c.max, displayWidth(got))
		}
	}
}
