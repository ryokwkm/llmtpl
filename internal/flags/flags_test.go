package flags

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConf(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "llmtpl.conf")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseConf_正常(t *testing.T) {
	got, err := ParseConf(writeConf(t, "# コメント\n\nwiki = true\ncommit = false\n"))
	if err != nil {
		t.Fatalf("ParseConf が失敗: %v", err)
	}
	if len(got.Flags) != 2 || !got.Flags["wiki"] || got.Flags["commit"] {
		t.Errorf("パース結果が不正: %+v", got)
	}
	if got.BundleRoot != "" {
		t.Errorf("bundle_root を書いていないのに設定された: %q", got.BundleRoot)
	}
}

func TestParseConf_前後空白除去(t *testing.T) {
	got, err := ParseConf(writeConf(t, "  commit   =   true  \n"))
	if err != nil {
		t.Fatalf("ParseConf が失敗: %v", err)
	}
	if !got.Flags["commit"] {
		t.Errorf("空白除去が不正: %+v", got)
	}
}

func TestParseConf_ファイル無しは空(t *testing.T) {
	got, err := ParseConf(filepath.Join(t.TempDir(), "no-such.conf"))
	if err != nil {
		t.Fatalf("ファイル無しでエラー: %v", err)
	}
	if len(got.Flags) != 0 {
		t.Errorf("空でない: %+v", got)
	}
}

// conf パーサのエッジケースを固定する（INI 由来の書式を持ち込んだ場合の挙動を含む）。
func TestParseConf_エッジケース(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string // 空ならエラー無しを期待
		check   func(t *testing.T, c Conf)
	}{
		{
			name:    "CRLF は許容",
			content: "loop = true\r\n",
			check: func(t *testing.T, c Conf) {
				if !c.Flags["loop"] {
					t.Error("CRLF で loop=true にならない")
				}
			},
		},
		{
			name:    "重複キーは後勝ち",
			content: "commit = false\ncommit = true\n",
			check: func(t *testing.T, c Conf) {
				if !c.Flags["commit"] {
					t.Error("重複キーの後勝ちが効いていない")
				}
			},
		},
		{
			name:    "不正値はエラー",
			content: "commit = maybe\n",
			wantErr: "値は true / false のみ",
		},
		{
			name:    "大文字 True はエラー",
			content: "commit = True\n",
			wantErr: "値は true / false のみ",
		},
		{
			name:    "インラインコメントはエラー（値の一部と見なされる）",
			content: "commit = false # note\n",
			wantErr: "値は true / false のみ",
		},
		{
			name:    "等号なし行はエラー",
			content: "commit\n",
			wantErr: "= がありません",
		},
		{
			name:    "キー名が空はエラー",
			content: "= true\n",
			wantErr: "キー名が空",
		},
		{
			name:    "BOM 付き先頭行のキーは commit として読まれない",
			content: "\ufeffcommit = false\n",
			check: func(t *testing.T, c Conf) {
				// BOM はキー名の一部になるため、そのキーは未知フラグとして Validate で弾かれる
				if _, ok := c.Flags["commit"]; ok {
					t.Error("BOM 付きキーが commit として読まれてしまう（Validate で検出できない）")
				}
			},
		},
		{
			// セクション付きの conf をそのまま持ち込む事故を黙って通さない（セクション内の上書きが失われる）
			name:    "セクション行はエラー（llmtpl の conf は平坦）",
			content: "[global]\nwiki = true\n",
			wantErr: "セクション",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseConf(writeConf(t, c.content))
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("エラーを期待したが nil（結果: %+v）", got)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("エラーメッセージが期待と異なる: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("予期しないエラー: %v", err)
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
}

func TestParseConf_エラーには行番号とパスが入る(t *testing.T) {
	path := writeConf(t, "wiki = true\ncommit = maybe\n")
	_, err := ParseConf(path)
	if err == nil {
		t.Fatal("エラーを期待したが nil")
	}
	if !strings.Contains(err.Error(), ":2") || !strings.Contains(err.Error(), filepath.Base(path)) {
		t.Errorf("エラーに行番号 / ファイル名が含まれない: %v", err)
	}
}

// 予約キー bundle_root は Set に入らない（Validate と Root.Flags の母集合が無改造で成立する前提）。
func TestParseConf_予約キーはフラグに混ざらない(t *testing.T) {
	got, err := ParseConf(writeConf(t, "bundle_root = /tmp\nwiki = true\n"))
	if err != nil {
		t.Fatalf("ParseConf が失敗: %v", err)
	}
	if _, ok := got.Flags[KeyBundleRoot]; ok {
		t.Fatal("予約キーが Flags に混ざった（Validate が未知フラグとして落とす / テンプレから見えてしまう）")
	}
	if len(got.Flags) != 1 || !got.Flags["wiki"] {
		t.Errorf("フラグ側が不正: %+v", got.Flags)
	}
	if got.BundleRoot != "/tmp" || got.BundleRootLine != 1 {
		t.Errorf("bundle_root が不正: %q line=%d", got.BundleRoot, got.BundleRootLine)
	}
}

func TestParseConf_予約キーのパス解決(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("ホームディレクトリを取得できない")
	}
	cases := []struct {
		name    string
		val     string
		want    string // %s は conf のディレクトリに置換する
		wantErr string
	}{
		{name: "絶対パスはそのまま", val: "/opt/bundles", want: "/opt/bundles"},
		{name: "末尾スラッシュは Clean される", val: "/opt/bundles/", want: "/opt/bundles"},
		{name: "相対パスは conf のディレクトリ基準", val: "../shared/llm-tpl", want: "%s/../shared/llm-tpl"},
		{name: "カレント相対も conf 基準", val: "./llm-tpl", want: "%s/llm-tpl"},
		{name: "~ は展開する", val: "~/cfg/llm-tpl", want: filepath.Join(home, "cfg/llm-tpl")},
		{name: "~ 単独も展開する", val: "~", want: home},
		{name: "~ユーザー名 はエラー", val: "~someone/x", wantErr: "~ユーザー名"},
		{name: "空値はエラー", val: "", wantErr: "値が空"},
		{name: "true はエラー", val: "true", wantErr: "パスを書くキー"},
		{name: "false はエラー", val: "false", wantErr: "パスを書くキー"},
		{name: "$VAR は展開しない（そのまま相対パス扱い）", val: "$HOME/x", want: "%s/$HOME/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeConf(t, KeyBundleRoot+" = "+c.val+"\n")
			got, err := ParseConf(path)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("エラーを期待したが nil: %+v", got)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("エラーメッセージが期待と異なる: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("予期しないエラー: %v", err)
			}
			want := strings.ReplaceAll(c.want, "%s", filepath.Dir(path))
			if got.BundleRoot != filepath.Clean(want) {
				t.Errorf("解決結果が不正: got %q want %q", got.BundleRoot, filepath.Clean(want))
			}
		})
	}
}

func TestParseConf_予約キーの重複は後勝ち(t *testing.T) {
	got, err := ParseConf(writeConf(t, "bundle_root = /a\nbundle_root = /b\n"))
	if err != nil {
		t.Fatalf("ParseConf が失敗: %v", err)
	}
	if got.BundleRoot != "/b" || got.BundleRootLine != 2 {
		t.Errorf("後勝ちが効いていない: %q line=%d", got.BundleRoot, got.BundleRootLine)
	}
}

// 分岐を「値が true/false でないか」で書くと、この検証が丸ごと死ぬ。
func TestParseConf_予約キー導入後もタイポは弾かれる(t *testing.T) {
	_, err := ParseConf(writeConf(t, "bundle_root = /tmp\ncommit = mayb\n"))
	if err == nil {
		t.Fatal("フラグのタイポがエラーにならない（予約キーの分岐がキー名でなく値を見ている疑い）")
	}
	if !strings.Contains(err.Error(), "値は true / false のみ") {
		t.Errorf("エラーメッセージが期待と異なる: %v", err)
	}
}

func TestIsReserved(t *testing.T) {
	if !IsReserved(KeyBundleRoot) {
		t.Error("bundle_root が予約キーと判定されない")
	}
	for _, k := range []string{"wiki", "commit", "bundleroot", "bundle-root", ""} {
		if IsReserved(k) {
			t.Errorf("%q が予約キーと誤判定された", k)
		}
	}
}

func TestMerge_後勝ち(t *testing.T) {
	base := Set{"wiki": true, "commit": true, "loop": false}
	over := Set{"commit": false, "loop": true}

	got := Merge(base, over)
	if got["wiki"] != true || got["commit"] != false || got["loop"] != true {
		t.Errorf("後勝ちが不正: %+v", got)
	}
	// 元のマップを壊さない（呼び出し側が defaults を再利用するため）
	if base["commit"] != true || len(base) != 3 {
		t.Errorf("base が変更された: %+v", base)
	}
	if len(over) != 2 {
		t.Errorf("over が変更された: %+v", over)
	}
}

func TestMerge_nil許容(t *testing.T) {
	if got := Merge(nil, Set{"a": true}); !got["a"] {
		t.Errorf("base=nil のマージが不正: %+v", got)
	}
	if got := Merge(Set{"a": true}, nil); !got["a"] {
		t.Errorf("over=nil のマージが不正: %+v", got)
	}
	if got := Merge(nil, nil); got == nil || len(got) != 0 {
		t.Errorf("nil,nil は空 Set を返すべき: %+v", got)
	}
}

func TestValidate_未知フラグはエラー(t *testing.T) {
	known := map[string]bool{"wiki": true, "commit": true}

	if err := Validate(Set{"wiki": true, "commit": false}, known, "conf"); err != nil {
		t.Errorf("既知フラグのみでエラー: %v", err)
	}

	err := Validate(Set{"wiki": true, "typo": true, "zzz": false}, known, "target/llmtpl.conf")
	if err == nil {
		t.Fatal("未知フラグがエラーになりません")
	}
	msg := err.Error()
	// 未知キーは名前を挙げる。列挙は決定的（ソート順）
	if !strings.Contains(msg, "typo") || !strings.Contains(msg, "zzz") {
		t.Errorf("未知フラグ名がメッセージに無い: %v", err)
	}
	if strings.Index(msg, "typo") > strings.Index(msg, "zzz") {
		t.Errorf("未知フラグの列挙がソートされていない: %v", err)
	}
	if !strings.Contains(msg, "target/llmtpl.conf") {
		t.Errorf("どの conf かがメッセージに無い: %v", err)
	}
	// 既知フラグ名も候補として示す（タイポ修正の手掛かり）
	if !strings.Contains(msg, "wiki") {
		t.Errorf("既知フラグの一覧がメッセージに無い: %v", err)
	}
}

func TestValidate_空Setは常に通る(t *testing.T) {
	if err := Validate(nil, map[string]bool{"wiki": true}, "conf"); err != nil {
		t.Errorf("nil Set でエラー: %v", err)
	}
	if err := Validate(Set{}, nil, "conf"); err != nil {
		t.Errorf("空 Set・既知なしでエラー: %v", err)
	}
}

func TestValidate_既知フラグが空なら全部未知(t *testing.T) {
	if err := Validate(Set{"wiki": true}, nil, "conf"); err == nil {
		t.Error("バンドルが 0 個のとき、フラグ指定はエラーであるべき")
	}
}

// 利用可能フラグの列挙も決定的（ソート順）
func TestValidate_利用可能フラグの列挙がソートされる(t *testing.T) {
	err := Validate(Set{"typo": true}, map[string]bool{"wiki": true, "commit": true, "loop": true}, "conf")
	if err == nil {
		t.Fatal("未知フラグがエラーになりません")
	}
	if !strings.Contains(err.Error(), "commit, loop, wiki") {
		t.Errorf("利用可能フラグがソート順で出ていない: %v", err)
	}
}
