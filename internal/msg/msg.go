// Package msg は llmtpl がユーザーへ出す文言を 1 か所へ集約する。
//
// 言語ごとに Catalog を 1 つ定義し（ja.go / en.go）、起動時に M へ選ぶ。
// map ではなく構造体で持つのは、**翻訳が 1 本欠けたらコンパイルエラーにする**ため。
// map だと欠落が実行時の空文字として出るまで気づけない。
//
// ⚠️ 生成物へ埋め込まれる文言（apply の GENERATED ヘッダ）はここに置かない。
// ファイルの内容がマシンのロケールに依存すると、環境ごとにヘッダが揺れて git で衝突する。
package msg

import "strings"

// Catalog は llmtpl が出力する全文言。パッケージ単位で入れ子にする。
type Catalog struct {
	State   StateMsg
	JSON    JSONMsg
	Render  RenderMsg
	FileOut FileOutMsg
	Link    LinkMsg
	Flags   FlagsMsg
	Bundle  BundleMsg
}

// M は選択済みのカタログ。プロセス起動時に環境変数から決まる。
var M = pick(env("LLMTPL_LANG"), env("LC_ALL"), env("LC_MESSAGES"), env("LANG"))

// pick は先に見つかった非空のロケール指定で言語を決める。
// ja で始まれば日本語、それ以外（未設定を含む）は英語。
func pick(vals ...string) Catalog {
	for _, v := range vals {
		if v == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(v), "ja") {
			return ja
		}
		return en
	}
	return en
}
