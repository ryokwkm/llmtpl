package msg

import (
	"regexp"
	"strings"
)

// verb は fmt の変換指定（%s / %d / %q / %w / %%）にざっくり一致する。
var verb = regexp.MustCompile(`%%|%[^a-zA-Z%]*[a-zA-Z]`)

// Lit は書式文字列から最も長いリテラル部分を返す。
//
// テストが「どのメッセージか」を言語に依存せず照合するためのもの。
// wantErr に日本語を直書きすると、英語ロケールの CI で必ず落ちる。
func Lit(format string) string {
	var best string
	for _, seg := range verb.Split(format, -1) {
		if s := strings.TrimSpace(seg); len(s) > len(best) {
			best = s
		}
	}
	return best
}
