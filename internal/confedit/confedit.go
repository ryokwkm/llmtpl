// Package confedit はターゲットの llmtpl.conf を**行単位で** in-place 書き換えする。
//
// 対話モードが選択結果を conf へ戻すための唯一の経路。テンプレの生成物と違い、conf は
// **人が書いたファイル**なので再生成してはいけない —— 実運用の conf はフラグ 1 行に対して
// 数十行の理由コメントが付いており（なぜ ON にしたか・なぜ ON にしてはいけないか）、
// 平坦な key=value として読み書きすると、その情報が丸ごと消し飛ぶ。
//
// そこで書き換えは 2 種類だけに絞る:
//
//	置換 … 既にある行の**値の文字だけ**を差し替える（桁揃え・インデント・行末まで保つ）
//	追記 … 末尾へ `name = true|false` を名前順で足す
//
// **行の削除はしない**。「明示 false」と「行なし（既定 false）」は実効値としては同じだが、
// 消すと周辺のコメントが宙に浮く。値としては冗長でも、書いた人の意図の記録として残す。
//
// Plan と Rewrite はどちらも純関数（I/O もロケールも知らない）。実効値の意味で正しいことは
// **実パーサ（flags.ParseConf）へ通し直す round-trip テスト**で担保している。
package confedit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ryokwkm/llmtpl/internal/flags"
	"github.com/ryokwkm/llmtpl/internal/msg"
)

// Change は 1 フラグ分の書き換え。Old が nil なら既存行が無い（= 追記）。
type Change struct {
	Name string
	Old  *bool
	New  bool
}

// Plan は「選択結果を満たす最小の変更集合」を名前順で返す。
//
// desired は UI の選択結果、conf はターゲット llmtpl.conf に**書かれている行**だけ、
// defaults はバンドルルートの defaults.conf。names はフラグの母集合（= バンドル名の一覧）で、
// 列挙を決定的にするために呼び出し側から渡す。
//
// 規則は 4 つ。要点は「既定値で足りるなら書かない」——
// conf に書けば書くほど defaults.conf の変更が届かなくなるので、差分は最小に保つ。
func Plan(desired, conf, defaults flags.Set, names []string) []Change {
	var out []Change
	for _, name := range slices.Sorted(slices.Values(names)) {
		want := desired[name]
		cur, hasLine := conf[name]
		switch {
		case hasLine && cur == want:
			// 既に望みどおり。行にも周辺のコメントにも触らない
		case hasLine:
			out = append(out, Change{Name: name, Old: &cur, New: want})
		case want != defaults[name]:
			// 行が無く、既定値では望みに届かないときだけ足す
			out = append(out, Change{Name: name, New: want})
		}
	}
	return out
}

// Rewrite は src へ changes を適用した新しい conf テキストを返す。
//
// 置換対象の行が見つからなければエラー（黙って追記へ倒さない）。Plan が Old 非 nil を返すのは
// 行があったときだけなので、ここに来るのは「conf を読んでから書くまでに中身が変わった」場合で、
// 追記で取り繕うと二重定義を作る。
func Rewrite(src string, changes []Change) (string, error) {
	if len(changes) == 0 {
		return src, nil
	}

	lines := strings.Split(src, "\n")
	// 末尾が改行なら Split の最後の要素は ""。追記位置の計算から外し、最後に戻す
	trailingNL := len(lines) > 0 && lines[len(lines)-1] == ""
	if trailingNL {
		lines = lines[:len(lines)-1]
	}

	var appends []Change
	for _, c := range changes {
		if c.Old == nil {
			appends = append(appends, c)
			continue
		}
		i := lastLineOf(lines, c.Name)
		if i < 0 {
			return "", fmt.Errorf(msg.M.ConfEdit.LineVanished, c.Name)
		}
		lines[i] = replaceValue(lines[i], boolText(c.New))
	}

	if len(appends) > 0 {
		slices.SortFunc(appends, func(a, b Change) int { return strings.Compare(a.Name, b.Name) })
		nl := lineEnding(lines)
		for _, c := range appends {
			lines = append(lines, fmt.Sprintf("%s = %s%s", c.Name, boolText(c.New), nl))
		}
		trailingNL = true // 追記した行は必ず改行で閉じる
	}

	out := strings.Join(lines, "\n")
	if trailingNL {
		out += "\n"
	}
	return out, nil
}

// lastLineOf は key を定義している**最後の**行の位置を返す（無ければ -1）。
// ParseConf が重複を後勝ちで読むので、最後の 1 行を直せば実効値が確定する。
func lastLineOf(lines []string, key string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if k, ok := lineKey(lines[i]); ok && k == key {
			return i
		}
	}
	return -1
}

// lineKey は行が定義しているフラグ名を返す。コメント・空行・予約キーは対象外。
// **判定は ParseConf と同じ規則**にする（片方だけが行と認める状態を作らない）。
func lineKey(line string) (string, bool) {
	text := strings.TrimSpace(line)
	if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, "[") {
		return "", false
	}
	key, _, ok := strings.Cut(text, "=")
	if !ok {
		return "", false
	}
	key = strings.TrimSpace(key)
	if key == "" || flags.IsReserved(key) {
		return "", false
	}
	return key, true
}

// replaceValue は `key = value` の **value の文字だけ**を差し替える。
// 行を組み立て直さないので、桁揃え（`commit    = true`）・インデント・行末の空白や \r が残る。
func replaceValue(line, val string) string {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return line // lineKey を通った行なので到達しない
	}
	head, rest := line[:eq+1], line[eq+1:]
	lead := rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
	tail := rest[len(strings.TrimRight(rest, " \t\r")):]
	return head + lead + val + tail
}

// lineEnding は追記する行の改行コードを既存の行から決める（CRLF の conf を混在させない）。
func lineEnding(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] != "" {
			if strings.HasSuffix(lines[i], "\r") {
				return "\r"
			}
			return ""
		}
	}
	return ""
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
