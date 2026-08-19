package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryokwkm/llmtpl/internal/bundle"
)

// examples/ のサンプルを実物として走らせ、README に載せた出力と一致することを固定する。
// サンプルは「動くこと」だけが価値なので、腐ったら落ちるようにしておく。
//
// 名前付きブロックの例は **フラグを OFF にしても落ちない** ことが要点なので ON / OFF の両方を通す。
// ターゲット側で {{template}} を {{if}} で包み忘れると、OFF のとき
// 「template ... not defined」で Apply ごと失敗するため、OFF 側がその番人になる。
func TestExample_名前付きブロックがONとOFFの両方で期待どおり(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("..", "..", "examples", "named-blocks"))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err := os.CopyFS(work, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	projRoot := filepath.Join(work, "proj")
	targetDir := filepath.Join(projRoot, bundle.DefaultOverlay)
	root, err := LoadRoot(filepath.Join(work, BundleDirName))
	if err != nil {
		t.Fatalf("LoadRoot が失敗: %v", err)
	}

	cases := []struct {
		name     string
		conf     string
		expected string
	}{
		{"ON", "styleguide = true\n", "expected-on.md"},
		{"OFF", "styleguide = false\n", "expected-off.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(projRoot, TargetConfName), []byte(c.conf), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := root.Apply(Target{Dir: projRoot}, Options{}); err != nil {
				t.Fatalf("Apply が失敗: %v", err)
			}

			gotAll, err := os.ReadFile(filepath.Join(targetDir, "CLAUDE.md"))
			if err != nil {
				t.Fatal(err)
			}
			// 1 行目の GENERATED ヘッダは原本パスを含むので比較対象外
			_, got, _ := strings.Cut(string(gotAll), "\n")

			want, err := os.ReadFile(filepath.Join(work, c.expected))
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s と一致しません。\n--- want ---\n%s\n--- got ---\n%s", c.expected, want, got)
			}
		})
	}
}
