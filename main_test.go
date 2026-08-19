package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExitCode(t *testing.T) {
	if got := exitCode(diffErr{n: 2}); got != 2 {
		t.Errorf("check の差分は exit 2: %d", got)
	}
	if got := exitCode(errStub{}); got != 1 {
		t.Errorf("通常のエラーは exit 1: %d", got)
	}
}

type errStub struct{}

func (errStub) Error() string { return "stub" }

// 位置引数とオプションの混在は cobra（pflag）が扱う。自作の再パースは持たない。
func TestサブコマンドはオプションとDirの混在を受け付ける(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"get", "wiki", "-C", "/no-such-dir", "--tpl-home", "/no-such-home"})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})

	// 解析自体は通り、実行時に「バンドルルートが無い」で落ちる（引数の形では落ちない）
	err := cmd.Execute()
	if err == nil {
		t.Fatal("存在しないルートでエラーになるはず")
	}
	if strings.Contains(err.Error(), "flag") || strings.Contains(err.Error(), "arg") {
		t.Errorf("引数解析で落ちている: %v", err)
	}
}

func TestRel_cwd配下は相対表示(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if got := rel(filepath.Join(dir, "a", "b")); got != filepath.Join("a", "b") {
		t.Errorf("相対表示になっていない: %q", got)
	}
	// cwd の外は絶対パスのまま（.. を並べない）
	outside := t.TempDir()
	if got := rel(outside); got != outside {
		t.Errorf("cwd 外は絶対パスのままにすべき: %q", got)
	}
}

// 表示幅を数えないと日本語の列見出しで桁がずれる（text/tabwriter を使わない理由）。
func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"ON", 2},
		{"ターゲット", 10}, // 全角 5 文字
		{"バンドル", 8},
		{"a漢字z", 6},
	}
	for _, c := range cases {
		if got := displayWidth(c.s); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}
