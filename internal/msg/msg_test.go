package msg

import (
	"reflect"
	"testing"
)

func TestPick_ロケールの優先順(t *testing.T) {
	// 引数は pick が見る順（LLMTPL_LANG, LC_ALL, LC_MESSAGES, LANG）。
	cases := []struct {
		name   string
		vals   []string
		wantJA bool
	}{
		{"LLMTPL_LANG が最優先", []string{"ja", "en_US.UTF-8", "", "en_US.UTF-8"}, true},
		{"LLMTPL_LANG=en は LANG=ja に勝つ", []string{"en", "", "", "ja_JP.UTF-8"}, false},
		{"LANG が ja なら日本語", []string{"", "", "", "ja_JP.UTF-8"}, true},
		{"LC_ALL は LANG に勝つ", []string{"", "en_US.UTF-8", "", "ja_JP.UTF-8"}, false},
		{"LC_MESSAGES も見る", []string{"", "", "ja_JP.UTF-8", "en_US.UTF-8"}, true},
		{"全部未設定なら英語", []string{"", "", "", ""}, false},
		{"C ロケールは英語", []string{"", "", "", "C"}, false},
		{"大文字小文字は区別しない", []string{"JA", "", "", ""}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pick(c.vals...)
			gotJA := got.Cmd.Short == ja.Cmd.Short
			if gotJA != c.wantJA {
				t.Errorf("日本語=%v を期待したが %v", c.wantJA, gotJA)
			}
		})
	}
}

// 構造体なのでフィールドの欠落はコンパイルが弾くが、空文字での埋め忘れは弾けない。
func TestCatalog_空の文言が無い(t *testing.T) {
	for _, c := range []struct {
		lang string
		cat  Catalog
	}{{"ja", ja}, {"en", en}} {
		walkStrings(t, c.lang, reflect.ValueOf(c.cat), reflect.TypeOf(c.cat).Name())
	}
}

func walkStrings(t *testing.T, lang string, v reflect.Value, path string) {
	t.Helper()
	for i := range v.NumField() {
		f, name := v.Field(i), path+"."+v.Type().Field(i).Name
		switch f.Kind() {
		case reflect.Struct:
			walkStrings(t, lang, f, name)
		case reflect.String:
			if f.String() == "" {
				t.Errorf("%s: %s が空です", lang, name)
			}
		}
	}
}

func TestLit_最長のリテラル部分を返す(t *testing.T) {
	// 前後の空白だけを落とすので、書式の都合で先頭に : が残ることがある（照合には影響しない）
	cases := []struct{ format, want string }{
		{"%s:%d: 値は true / false のみ: %s = %s", ": 値は true / false のみ:"},
		{"%s:%d: the value must be true or false: %s = %s", ": the value must be true or false:"},
		{"%[2]s in %[1]s", "in"},
		{"cannot read %s: %w", "cannot read"},
		{"%s%d", ""},
	}
	for _, c := range cases {
		if got := Lit(c.format); got != c.want {
			t.Errorf("Lit(%q) = %q, want %q", c.format, got, c.want)
		}
	}
}
