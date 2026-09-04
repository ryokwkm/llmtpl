# examples

[English](README.md) | **日本語**

README に載せていない使い方のサンプル。**どれも上級の逃げ道**で、基本の使い方は
リポジトリルートの [README.ja.md](../README.ja.md) にある。

各ディレクトリはそれ単体で動く（`--tpl-home ./llm-tpl` を渡して `apply` する）。
出力は `expected-*.md` に固定してあり、`go test ./...` が実物を実行して照合するので腐らない。

| | 何ができるか |
|---|---|
| [named-blocks](named-blocks/) | 1 つのバンドルから複数の断片を、ターゲットの好きな場所へ差し込む |
