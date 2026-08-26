package confedit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ryokwkm/llmtpl/internal/flags"
)

func boolp(b bool) *bool { return &b }

// names はテストで使うバンドル名の母集合（実際の呼び出し元も名前順で渡す）。
var names = []string{"auto-sync", "commit", "loop", "wiki"}

func TestPlan_４条の規則(t *testing.T) {
	cases := []struct {
		name     string
		defaults flags.Set
		conf     flags.Set
		desired  flags.Set
		want     []Change
	}{
		{
			name:     "既存行と一致するなら何もしない",
			defaults: flags.Set{},
			conf:     flags.Set{"commit": true},
			desired:  flags.Set{"commit": true},
			want:     nil,
		},
		{
			name:     "既存行の false を一致させても何もしない",
			defaults: flags.Set{},
			conf:     flags.Set{"commit": false},
			desired:  flags.Set{"commit": false},
			want:     nil,
		},
		{
			name:     "既存行と違えば値を置換する",
			defaults: flags.Set{},
			conf:     flags.Set{"commit": true},
			desired:  flags.Set{"commit": false},
			want:     []Change{{Name: "commit", Old: boolp(true), New: false}},
		},
		{
			name:     "行が無く既定と同じなら書かない",
			defaults: flags.Set{"commit": true},
			conf:     flags.Set{},
			desired:  flags.Set{"commit": true},
			want:     nil,
		},
		{
			name:     "行が無く既定が未設定で OFF のままなら書かない",
			defaults: flags.Set{},
			conf:     flags.Set{},
			desired:  flags.Set{"commit": false},
			want:     nil,
		},
		{
			name:     "既定 ON を OFF にするなら明示的に追記する",
			defaults: flags.Set{"commit": true},
			conf:     flags.Set{},
			desired:  flags.Set{"commit": false},
			want:     []Change{{Name: "commit", Old: nil, New: false}},
		},
		{
			name:     "既定 OFF を ON にするなら追記する",
			defaults: flags.Set{},
			conf:     flags.Set{},
			desired:  flags.Set{"commit": true},
			want:     []Change{{Name: "commit", Old: nil, New: true}},
		},
		{
			name:     "既定 ON を明示 false で殺してある行を ON へ戻す",
			defaults: flags.Set{"commit": true},
			conf:     flags.Set{"commit": false},
			desired:  flags.Set{"commit": true},
			want:     []Change{{Name: "commit", Old: boolp(false), New: true}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Plan(tc.desired, tc.conf, tc.defaults, []string{"commit"})
			assertChanges(t, got, tc.want)
		})
	}
}

func TestPlan_順序は名前順で決まる(t *testing.T) {
	got := Plan(
		flags.Set{"wiki": true, "auto-sync": true, "commit": true, "loop": true},
		flags.Set{},
		flags.Set{},
		names,
	)
	var order []string
	for _, c := range got {
		order = append(order, c.Name)
	}
	want := []string{"auto-sync", "commit", "loop", "wiki"}
	if !slices.Equal(order, want) {
		t.Errorf("順序が名前順でない\n got: %v\nwant: %v", order, want)
	}
}

func TestPlan_desiredに無い名前は触らない(t *testing.T) {
	// UI が全バンドルを列挙する前提だが、欠けていても既定値の扱い（= false）に倒れるだけで
	// 既存行を壊さないことを固定する
	got := Plan(flags.Set{}, flags.Set{"commit": true}, flags.Set{}, []string{"commit"})
	want := []Change{{Name: "commit", Old: boolp(true), New: false}}
	assertChanges(t, got, want)
}

func TestRewrite_値の置換で他の行はbyte一致(t *testing.T) {
	src := realisticConf()
	got, err := Rewrite(src, []Change{{Name: "loop", Old: boolp(true), New: false}})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}

	srcLines := strings.Split(src, "\n")
	gotLines := strings.Split(got, "\n")
	if len(srcLines) != len(gotLines) {
		t.Fatalf("行数が変わった: %d → %d", len(srcLines), len(gotLines))
	}
	changed := 0
	for i := range srcLines {
		if srcLines[i] == gotLines[i] {
			continue
		}
		changed++
		if !strings.Contains(gotLines[i], "loop") {
			t.Errorf("%d 行目が意図せず変わった\n got: %q\nwant: %q", i+1, gotLines[i], srcLines[i])
		}
	}
	if changed != 1 {
		t.Errorf("変わった行数 = %d, want 1", changed)
	}
}

func TestRewrite_桁揃えとインデントを保つ(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "= の前で桁を揃えてある",
			src:  "commit    = true\nloop      = true\nauto-sync = true\n",
			want: "commit    = true\nloop      = false\nauto-sync = true\n",
		},
		{
			name: "= の周りに空白が無い",
			src:  "loop=true\n",
			want: "loop=false\n",
		},
		{
			name: "行頭にインデントがある",
			src:  "  loop = true\n",
			want: "  loop = false\n",
		},
		{
			name: "値の後ろに空白がある",
			src:  "loop = true   \n",
			want: "loop = false   \n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Rewrite(tc.src, []Change{{Name: "loop", Old: boolp(true), New: false}})
			if err != nil {
				t.Fatalf("Rewrite: %v", err)
			}
			if got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestRewrite_追記は末尾へ名前順で並ぶ(t *testing.T) {
	src := "# 先頭のコメント\ncommit = true\n"
	got, err := Rewrite(src, []Change{
		{Name: "wiki", New: true},
		{Name: "auto-sync", New: true},
	})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	want := "# 先頭のコメント\ncommit = true\nauto-sync = true\nwiki = true\n"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestRewrite_末尾に改行が無いsrcへ追記する(t *testing.T) {
	got, err := Rewrite("commit = true", []Change{{Name: "wiki", New: true}})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	want := "commit = true\nwiki = true\n"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestRewrite_末尾の改行の有無は置換では変えない(t *testing.T) {
	got, err := Rewrite("commit = true", []Change{{Name: "commit", Old: boolp(true), New: false}})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if want := "commit = false"; got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestRewrite_空のsrcから生成する(t *testing.T) {
	got, err := Rewrite("", []Change{{Name: "commit", New: true}})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if want := "commit = true\n"; got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestRewrite_変更ゼロなら元のまま(t *testing.T) {
	src := realisticConf()
	got, err := Rewrite(src, nil)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if got != src {
		t.Error("変更ゼロなのに内容が変わった")
	}
}

func TestRewrite_予約キーの行は触らない(t *testing.T) {
	// bundle_root はフラグと名前空間が別。フラグ名として同名が来ても行を奪わない
	src := "bundle_root = ../shared\ncommit = true\n"
	got, err := Rewrite(src, []Change{{Name: "commit", Old: boolp(true), New: false}})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	want := "bundle_root = ../shared\ncommit = false\n"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestRewrite_コメント中のキーらしき行は触らない(t *testing.T) {
	src := "#   loop = true  ← 昔の設定\nloop = true\n"
	got, err := Rewrite(src, []Change{{Name: "loop", Old: boolp(true), New: false}})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	want := "#   loop = true  ← 昔の設定\nloop = false\n"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestRewrite_重複キーは最後の行だけ置換する(t *testing.T) {
	// ParseConf は後勝ちなので、最後の 1 行を直せば意味が確定する。
	// 手前の行は既に無効なので触らない（書いた人の履歴として残す）
	src := "loop = true\n# 二重定義\nloop = true\n"
	got, err := Rewrite(src, []Change{{Name: "loop", Old: boolp(true), New: false}})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	want := "loop = true\n# 二重定義\nloop = false\n"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestRewrite_CRLFを保つ(t *testing.T) {
	src := "# コメント\r\ncommit = true\r\n"
	got, err := Rewrite(src, []Change{
		{Name: "commit", Old: boolp(true), New: false},
		{Name: "wiki", New: true},
	})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	want := "# コメント\r\ncommit = false\r\nwiki = true\r\n"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestRewrite_置換対象の行が無ければエラー(t *testing.T) {
	// Plan が Old 非 nil を返すのは行があったときだけなので、ここへ来るのは呼び出し側のバグ。
	// 黙って追記に倒すと「conf を読み直さずに書いた」事故を隠すのでエラーにする
	if _, err := Rewrite("commit = true\n", []Change{{Name: "loop", Old: boolp(true), New: false}}); err == nil {
		t.Fatal("エラーになるべき")
	}
}

// TestRewrite_書き換え後の実効値がdesiredに一致する は実パーサを正として使う round-trip 検証。
// Plan と Rewrite の規則が「実効値」の意味で正しいことを、apply.Root.Flags と同じ式で確かめる。
func TestRewrite_書き換え後の実効値がdesiredに一致する(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		defaults flags.Set
		desired  flags.Set
	}{
		{
			name:     "全部 ON",
			src:      realisticConf(),
			defaults: flags.Set{"commit": false},
			desired:  flags.Set{"auto-sync": true, "commit": true, "loop": true, "wiki": true},
		},
		{
			name:     "全部 OFF",
			src:      realisticConf(),
			defaults: flags.Set{"commit": false},
			desired:  flags.Set{"auto-sync": false, "commit": false, "loop": false, "wiki": false},
		},
		{
			name:     "既定 ON のものを全部 OFF にする",
			src:      realisticConf(),
			defaults: flags.Set{"commit": true, "wiki": true, "loop": true, "auto-sync": true},
			desired:  flags.Set{"auto-sync": false, "commit": false, "loop": false, "wiki": false},
		},
		{
			name:     "conf が空の状態から作る",
			src:      "",
			defaults: flags.Set{"commit": true},
			desired:  flags.Set{"auto-sync": true, "commit": false, "loop": false, "wiki": true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conf := parseFlags(t, tc.src)
			changes := Plan(tc.desired, conf, tc.defaults, names)
			out, err := Rewrite(tc.src, changes)
			if err != nil {
				t.Fatalf("Rewrite: %v", err)
			}

			got := effective(t, out, tc.defaults)
			for _, n := range names {
				if got[n] != tc.desired[n] {
					t.Errorf("%s の実効値 = %v, want %v\n--- 書き換え後 ---\n%s",
						n, got[n], tc.desired[n], out)
				}
			}
		})
	}
}

func TestRewrite_冪等(t *testing.T) {
	src := realisticConf()
	defaults := flags.Set{"commit": true}
	desired := flags.Set{"auto-sync": false, "commit": false, "loop": false, "wiki": true}

	out, err := Rewrite(src, Plan(desired, parseFlags(t, src), defaults, names))
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	// 同じ desired で計画し直すと空になる（＝ 2 回目の apply が差分を作らない）
	if again := Plan(desired, parseFlags(t, out), defaults, names); len(again) != 0 {
		t.Errorf("2 回目の Plan が空でない: %+v\n--- 書き換え後 ---\n%s", again, out)
	}
}

// realisticConf は実運用の conf（コメントが本文の大半・桁揃え・予約キー）を模した fixture。
func realisticConf() string {
	return `# このターゲットで上書きするフラグ（既定値は llm-tpl/defaults.conf）。
#
# 形式は平坦な key = true|false。セクション（[global] 等）を書くとエラーになる。
#
#   commit   — 作業の一区切りで自動コミットする規約
#   loop     — ループ規約一式（rules 2 本 + Stop hook）
#
# ⚠️ **skill を配るバンドルはここで ON にしない**。user スコープと二重になる。
#    詳細は defaults.conf のコメントを参照。

bundle_root = ../llm-tpl

commit    = true
loop      = true
auto-sync = true
`
}

// parseFlags は src を一時ファイルへ書いて実パーサに通す（テストの中で自前パースをしない）。
func parseFlags(t *testing.T, src string) flags.Set {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llmtpl.conf")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("fixture の書き出し: %v", err)
	}
	conf, err := flags.ParseConf(path)
	if err != nil {
		t.Fatalf("ParseConf: %v", err)
	}
	return conf.Flags
}

// effective は apply.Root.Flags と同じ式で実効値を出す（母集合 → defaults → conf の後勝ち）。
func effective(t *testing.T, src string, defaults flags.Set) flags.Set {
	t.Helper()
	base := make(flags.Set, len(names))
	for _, n := range names {
		base[n] = false
	}
	return flags.Merge(flags.Merge(base, defaults), parseFlags(t, src))
}

func assertChanges(t *testing.T, got, want []Change) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("件数 = %d, want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i].Name != want[i].Name || got[i].New != want[i].New {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
			continue
		}
		switch {
		case got[i].Old == nil && want[i].Old == nil:
		case got[i].Old == nil || want[i].Old == nil:
			t.Errorf("[%d] Old の有無が違う: got %v, want %v", i, got[i].Old, want[i].Old)
		case *got[i].Old != *want[i].Old:
			t.Errorf("[%d] Old = %v, want %v", i, *got[i].Old, *want[i].Old)
		}
	}
}
