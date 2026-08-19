# 名前付きブロック — 1 バンドルから複数の断片を、好きな場所へ

**これは上級の逃げ道です。** 既定のやり方（バンドル直下に `CLAUDE.md.tmpl` を置く）で足りるなら、
そちらを使ってください。README には載せていません。

## 何ができるか

既定では、1 バンドルが CLAUDE.md へ寄稿できる断片は **1 つだけ**（`<バンドル>/CLAUDE.md.tmpl`）で、
差し込み先も 1 か所です（受け口 `{{- slot "<バンドル名>"}}`、無ければ末尾）。

「文体の規約は開発の節へ、用語の規約はテストの節へ」のように、**1 つのバンドルから複数の断片を
別々の場所へ**入れたいときは、`partials/` に名前付きブロックを定義してターゲット側から呼びます。

```
llm-tpl/styleguide/
  bundle.conf
  partials/blocks.tmpl      ← {{define "styleguide/tone"}} … {{end}}
proj/.claude/
  llmtpl.conf               ← styleguide = true
  CLAUDE.md.tmpl            ← {{- if .styleguide}}{{template "styleguide/tone" .}}{{end}}
```

ブロック名に**バンドル名を接頭辞として付けておく**と、複数のバンドルが `partials/` を持っても
名前が衝突しません。これが実質的な名前空間になります。

このバンドルは `CLAUDE.md.tmpl` を持ちません。`partials/` だけを持つバンドルでも、
ON になっていればブロックはターゲットから見えます。

## 試す

```sh
cd examples/named-blocks
llmtpl apply --tpl-home ./llm-tpl proj/.claude
cat proj/.claude/CLAUDE.md          # → expected-on.md と同じ本文

sed -i '' 's/true/false/' proj/.claude/llmtpl.conf
llmtpl apply --tpl-home ./llm-tpl proj/.claude
cat proj/.claude/CLAUDE.md          # → expected-off.md と同じ本文
```

## ⚠️ 必ず `{{if}}` で包むこと

**これが唯一にして最大の落とし穴です。**

`{{template "styleguide/tone" .}}` を裸で書くと、フラグを OFF にした瞬間に
`apply` が**丸ごと失敗します**（そのターゲットだけでなく、以降のターゲットも処理されません）。

```
llmtpl: テンプレートの評価に失敗: template "styleguide/tone" not defined
```

ブロックの定義はバンドルが ON のときしか読み込まれないのに、`{{template}}` は
定義が無いとエラーになるためです。受け口（`slot`）は寄稿が無ければ空文字を返すので
この問題が起きません —— **既定のやり方が受け口である理由がこれです。**

`{{- if .styleguide}}` で包めば、OFF のとき呼び出し自体が評価されないので安全に消えます。
この例はそう書いてあります。

## バンドル名にハイフンがあるとき

`{{if .auto-sync}}` は Go のテンプレートでは書けません（`bad character U+002D`）。
その場合は `{{if index . "auto-sync"}}` と書きます。

## 受け口とどちらを使うか

| | 受け口（既定） | 名前付きブロック |
|---|---|---|
| 1 バンドルから寄稿できる断片 | 1 つ | いくつでも |
| 差し込む場所 | 1 か所（受け口か末尾） | 好きなだけ |
| OFF にしたとき | 何も起きない | **`{{if}}` を忘れると apply が落ちる** |
| ターゲット側に要るもの | `{{- slot "名前"}}` 1 行（省略可） | `{{if}}` + `{{template}}` |

**迷ったら受け口。** 「1 バンドルの内容を、離れた複数の場所へ配りたい」という要求が実際に出てから、
こちらへ来てください。
