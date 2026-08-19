# testdata/golden — 合成 fixture

`internal/apply/golden_test.go` の入力と期待値。**自己完結した合成データ**で、どこかの実設定の
写しではない（追随義務は無い）。

```
llm-tpl/                     バンドルルート
  defaults.conf              hello / silent を常時 ON にする
  hello/                     CLAUDE.md 断片 + skills/
  review/                    CLAUDE.md 断片
  silent/                    bundle.conf のみ（CLAUDE.md 断片を持たない）
  off/                       CLAUDE.md 断片。どのケースでも OFF
  logcheck/                  CLAUDE.md 断片 + settings.json 断片 + rules/ + hooks/
                             （README のデモと同一。quickstart だけが ON にする）
cases/<name>/.claude/        ターゲット（CLAUDE.md.tmpl + 任意の llmtpl.conf）
cases/<name>/expected.md     期待する生成物（1 行目の GENERATED ヘッダを除いた本文）
```

各ケースが担う軸はテスト本体の doc コメントにある。ケースは `cases/` 直下のディレクトリを
自動列挙しているので、増減はディレクトリ操作だけで済む。

## 断片の中身に意味は無い

エンジンは断片の**中身を見ない**。`bundle.Compose` がするのは末尾改行を均して連結することだけで、
そこに書いてあるのが規約でもダミーでも経路は同一になる。このテストが固定しているのは合成の
**構造**（どの断片がどこへ入るか・入らないか）であって、文面ではない。

したがって fixture の文面は当たり障りのないダミーでよく、現実の設定へ寄せる意味は無い。

## quickstart ケースだけは README と紐づく

`cases/quickstart/` は `README.md` の「動かして見る」と同じ入力・同じ出力にしてある。
**README の出力例が実装とズレたらこのケースが落ちる**のが狙いで、それがこのケースの主目的。

README 側を変えたら、このケースの `CLAUDE.md.tmpl` と `expected.md` も揃える
（逆も同じ）。片方だけ変えるとテストが落ちるので、ズレたまま気づかないことは無い。

## 期待値の更新

`expected.md` は**手で書く**。生成物をコピーして貼ると、実装が壊れたときに壊れた出力が
そのまま期待値になり、テストが何も守らなくなる。

意図した変更で落ちたときは、まず「なぜその出力になるのか」を合成規則から説明できるかを
確かめてから書き換えること。
