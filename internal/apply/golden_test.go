package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryokwkm/llmtpl/internal/bundle"
)

// golden test: 合成 fixture（testdata/golden）を入力に、生成物の本文が `expected.md` と
// バイト単位で一致することを固定する（1 行目の GENERATED ヘッダのみ比較対象外。ヘッダは
// 機構のメタ情報でパスを含むため）。単体テストが経路ごとに見ている合成規則を、
// 「複数バンドル × 複数ターゲット」を一度に通した結果として押さえるのが役割。
//
// fixture は**自己完結**している（どこかの実設定の写しではない）。バンドルの断片は
// 内容を持たない当たり障りのないダミーで、エンジンは断片の中身を見ないので現実味は要らない。
//
//	llm-tpl/hello    … CLAUDE.md 断片 + skills/
//	llm-tpl/review   … CLAUDE.md 断片
//	llm-tpl/silent   … bundle.conf のみ（CLAUDE.md 断片を持たない）
//	llm-tpl/off      … CLAUDE.md 断片。どのケースでも OFF
//	llm-tpl/logcheck … CLAUDE.md 断片 + settings.json 断片 + rules/ + hooks/（README のデモと同一）
//	cases/<name>/.claude/     … ターゲット（CLAUDE.md.tmpl + 任意の llmtpl.conf）
//	cases/<name>/expected.md  … 期待する生成物（ヘッダ行を除いた本文）
//
// 3 ケースが担う軸:
//
//	quickstart … README の「動かして見る」と同一。受け口なし → 末尾追記。
//	             **README の出力例が腐ったらここが落ちる**のが主目的
//	slotted    … ファイル中腹の受け口（位置指定が効くこと）+ 末尾の受け口 +
//	             OFF バンドルの受け口が痕跡なく消えること
//	appended   … 受け口を 1 つも持たないターゲットで 2 つのバンドルが寄稿する。
//	             **追記順がバンドル名の昇順**（hello → review）であることをここで固定する
//	             （受け口が無いと書き順が存在しないので、規則が無ければ生成物が不安定になる）
//
// silent は defaults.conf で常時 ON。断片を持たないバンドルが生成物に何も足さないことを、
// 全ケースが同時に固定している。
func TestGolden_合成fixtureの生成物が固定と一致する(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("..", "..", "testdata", "golden"))
	if err != nil {
		t.Fatal(err)
	}
	caseDirs, err := os.ReadDir(filepath.Join(src, "cases"))
	if err != nil {
		t.Fatal(err)
	}

	for _, cd := range caseDirs {
		if !cd.IsDir() {
			continue
		}
		t.Run(cd.Name(), func(t *testing.T) {
			// testdata を汚さないよう一時ディレクトリへ複製してから適用する
			work := t.TempDir()
			if err := os.CopyFS(work, os.DirFS(src)); err != nil {
				t.Fatal(err)
			}
			targetDir := filepath.Join(work, "cases", cd.Name())
			claudeDir := filepath.Join(targetDir, bundle.DefaultOverlay)

			root, err := LoadRoot(filepath.Join(work, "llm-tpl"))
			if err != nil {
				t.Fatalf("LoadRoot が失敗: %v", err)
			}
			rep, err := root.Apply(Target{Dir: targetDir}, Options{})
			if err != nil {
				t.Fatalf("Apply が失敗: %v", err)
			}
			// quickstart は settings.json.tmpl も持つ（README のデモと同一に保つため）ので、
			// 生成対象の総数ではなく CLAUDE.md が含まれることを確認する
			found := false
			for _, tg := range rep.Targets {
				if tg.Dest == filepath.Join(claudeDir, "CLAUDE.md") {
					found = true
				}
			}
			if !found {
				t.Fatalf("CLAUDE.md が生成対象に含まれない: %+v", rep.Targets)
			}

			gotAll, err := os.ReadFile(filepath.Join(claudeDir, "CLAUDE.md"))
			if err != nil {
				t.Fatal(err)
			}
			header, got, ok := strings.Cut(string(gotAll), "\n")
			if !ok || !strings.Contains(header, GeneratedMarker) {
				t.Fatalf("GENERATED ヘッダがない: %q", header)
			}
			wantB, err := os.ReadFile(filepath.Join(targetDir, "expected.md"))
			if err != nil {
				t.Fatal(err)
			}
			if got != string(wantB) {
				t.Errorf("expected.md と一致しません。\n--- want ---\n%s\n--- got ---\n%s", wantB, got)
			}
		})
	}
}

// README の Quick Start が載せている生成物と、quickstart ケースの期待値が一致することを固定する。
//
// **fixture が実設定の写しでなくなった今、腐って実害が出る資産は README だけ**（公開物の顔であり、
// 初見が最初に実行する手順でもある）。README を実際に読んで比較するので、README 側だけ直しても
// fixture 側だけ直しても落ちる。
func TestGolden_READMEのQuickStart出力がfixtureと一致する(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	block, ok := fencedBlockWithMarker(string(readme), GeneratedMarker)
	if !ok {
		t.Fatal("README.md に GENERATED ヘッダで始まるコードブロックがありません（Quick Start の出力例が消えた？）")
	}
	// 1 行目のヘッダは原本パスを含み、README とテストの作業ディレクトリで必ず違うので比較しない
	_, got, _ := strings.Cut(block, "\n")

	want, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "cases", "quickstart", "expected.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("README の Quick Start の出力例が fixture とズレています（片方だけ直した？）。\n--- README ---\n%s\n--- expected.md ---\n%s", got, want)
	}
}

// fencedBlockWithMarker は ``` で囲まれたブロックのうち、1 行目に marker を含む最初のものを返す。
func fencedBlockWithMarker(s, marker string) (string, bool) {
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "```") {
			continue
		}
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "```") {
				end = j
				break
			}
		}
		if end < 0 {
			return "", false
		}
		if end > i+1 && strings.Contains(lines[i+1], marker) {
			return strings.Join(lines[i+1:end], "\n") + "\n", true
		}
		i = end
	}
	return "", false
}
