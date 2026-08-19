package mergejson

import (
	"strings"
	"testing"
)

func parse(t *testing.T, s string) map[string]any {
	t.Helper()
	m, err := Parse([]byte(s), "test")
	if err != nil {
		t.Fatalf("Parse が失敗: %v", err)
	}
	return m
}

// merged は base ← over をマージして整形済み JSON 文字列で返す
func merged(t *testing.T, base, over string) string {
	t.Helper()
	got, err := Merge(parse(t, base), parse(t, over), "over")
	if err != nil {
		t.Fatalf("Merge が失敗: %v", err)
	}
	b, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParse_不正なJSONはラベル付きエラー(t *testing.T) {
	_, err := Parse([]byte("{\"a\": }"), "llm-tpl/wiki/settings.json.tmpl")
	if err == nil {
		t.Fatal("不正 JSON がエラーになりません")
	}
	if !strings.Contains(err.Error(), "llm-tpl/wiki/settings.json.tmpl") {
		t.Errorf("どの断片かがメッセージに無い: %v", err)
	}
}

func TestParse_オブジェクト以外はエラー(t *testing.T) {
	for _, src := range []string{`[1,2]`, `"str"`, `42`, `null`} {
		if _, err := Parse([]byte(src), "x"); err == nil {
			t.Errorf("トップレベルが object でない %q がエラーになりません", src)
		}
	}
}

func TestParse_空はエラーにしない(t *testing.T) {
	m, err := Parse([]byte("{}\n"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("空オブジェクトが空でない: %+v", m)
	}
}

func TestMerge_オブジェクトは再帰マージ(t *testing.T) {
	got := merged(t,
		`{"env": {"A": "1", "B": "2"}}`,
		`{"env": {"B": "9", "C": "3"}}`)
	want := `{
  "env": {
    "A": "1",
    "B": "9",
    "C": "3"
  }
}
`
	if got != want {
		t.Errorf("再帰マージが不正:\n%s", got)
	}
}

func TestMerge_スカラーは後勝ち(t *testing.T) {
	got := merged(t, `{"sandbox": true, "keep": 1}`, `{"sandbox": false}`)
	if !strings.Contains(got, `"sandbox": false`) || !strings.Contains(got, `"keep": 1`) {
		t.Errorf("後勝ちが不正:\n%s", got)
	}
}

func TestMerge_配列は追加して重複排除(t *testing.T) {
	got := merged(t,
		`{"permissions": {"allow": ["Bash(make *)", "Bash(ssh *)"]}}`,
		`{"permissions": {"allow": ["Bash(ssh *)", "Bash(go *)"]}}`)
	want := `{
  "permissions": {
    "allow": [
      "Bash(make *)",
      "Bash(ssh *)",
      "Bash(go *)"
    ]
  }
}
`
	if got != want {
		t.Errorf("配列マージが不正（順序保持 + 重複排除）:\n%s", got)
	}
}

// hooks は要素がオブジェクトの配列。深い等価で重複排除する（同じ hook を 2 回登録しない）
func TestMerge_オブジェクト要素の配列も重複排除(t *testing.T) {
	frag := `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "verify"}]}]}}`
	got := merged(t, frag, frag)
	if strings.Count(got, `"command": "verify"`) != 1 {
		t.Errorf("同一要素が重複している:\n%s", got)
	}
}

func TestMerge_型が違えばエラー(t *testing.T) {
	cases := []struct{ base, over string }{
		{`{"a": {"x": 1}}`, `{"a": 1}`},          // object ← scalar
		{`{"a": [1]}`, `{"a": {"x": 1}}`},        // array ← object
		{`{"a": 1}`, `{"a": [1]}`},               // scalar ← array
		{`{"a": {"b": [1]}}`, `{"a": {"b": 1}}`}, // 深い位置
	}
	for _, c := range cases {
		_, err := Merge(parse(t, c.base), parse(t, c.over), "over")
		if err == nil {
			t.Errorf("型不一致がエラーになりません: %s ← %s", c.base, c.over)
			continue
		}
		if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "over") {
			t.Errorf("キー名 / 出典がメッセージに無い: %v", err)
		}
	}
}

func TestMerge_引数を変更しない(t *testing.T) {
	base := parse(t, `{"env": {"A": "1"}}`)
	over := parse(t, `{"env": {"A": "2"}}`)

	if _, err := Merge(base, over, "over"); err != nil {
		t.Fatal(err)
	}
	if base["env"].(map[string]any)["A"] != "1" {
		t.Errorf("base が変更された: %+v", base)
	}
}

func TestMerge_nil許容(t *testing.T) {
	got, err := Merge(nil, map[string]any{"a": float64(1)}, "over")
	if err != nil {
		t.Fatal(err)
	}
	if got["a"] != float64(1) {
		t.Errorf("base=nil のマージが不正: %+v", got)
	}
	got, err = Merge(map[string]any{"a": float64(1)}, nil, "over")
	if err != nil {
		t.Fatal(err)
	}
	if got["a"] != float64(1) {
		t.Errorf("over=nil のマージが不正: %+v", got)
	}
}

// 生成物が安定すること（キーはソート・2 スペース・末尾改行）
func TestFormat_決定的な整形(t *testing.T) {
	m := parse(t, `{"z": 1, "a": {"y": 2, "b": 3}}`)
	b, err := Format(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "a": {
    "b": 3,
    "y": 2
  },
  "z": 1
}
`
	if string(b) != want {
		t.Errorf("整形が不正:\n%s", b)
	}
	// 2 回目も同一（マップ反復順に依存しない）
	b2, _ := Format(m)
	if string(b2) != string(b) {
		t.Error("整形が非決定的")
	}
}

func TestFormat_数値は整数のまま出す(t *testing.T) {
	b, err := Format(parse(t, `{"timeout": 120}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"timeout": 120`) {
		t.Errorf("整数が浮動小数になっている: %s", b)
	}
}
