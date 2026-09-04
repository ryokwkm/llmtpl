# README のデモ GIF。**リポジトリルートから** `vhs assets/demo.tape` で録り直す。
#
# 見せる筋は 3 カット: ①1 機能が 4 ファイルに散っているのを 1 ディレクトリに畳んだ姿
# ②conf 1 行でターゲットへ配られる ③その 1 行を false に戻すと全部消える。
# 🔴 ③が最大のフック。価値は配る側ではなく**漏れなく消える側**にあるので、そこを撮らないと
# GIF を貼った意味が薄い。カットを足すときも ③を削らないこと。

Output assets/demo.gif

Require llmtpl
Require tree

Set Shell bash
Set FontSize 22
Set Width 1200
Set Height 700
Set Padding 20
Set TypingSpeed 75ms
Set Framerate 24

# 下ごしらえ。ここは映さない。
# - PS1 を固定するのは、録る人の bashrc のプロンプトが映り込むのを防ぐため
# - LLMTPL_LANG=en は必須。無いと録る人のロケール次第で日本語の出力が英語 README に載る
Hide
Set TypingSpeed 1ms
Type `export PS1='$ ' LLMTPL_LANG=en` Enter
Type `REPO=$PWD; DEMO=$(mktemp -d /tmp/llmtpl-demo.XXXXXX)` Enter
Type `sh "$REPO/assets/demo-setup.sh" "$DEMO" && cd "$DEMO"` Enter
Type `clear` Enter
Set TypingSpeed 75ms
Show

# ① 1 機能ぶんの 4 ファイルが、バンドル 1 ディレクトリに畳まれている
Type@30ms "# One feature. Four files. One directory." Enter
Sleep 800ms
Type "tree -a --noreport" Enter
Sleep 3s

Hide
Type "clear" Enter
Show

# ② ターゲット側は conf 1 行だけ
Type@30ms "# The project opts in with one line." Enter
Sleep 800ms
Type "echo 'logcheck = true' > proj/llmtpl.conf" Enter
Sleep 500ms
Type "cd proj && llmtpl apply" Enter
Sleep 2s
Type "tree -a --noreport .claude" Enter
Sleep 3s

Hide
Type "clear" Enter
Show

# ③ その 1 行を戻すと、足された 4 つが漏れなく出ていく
Type@30ms "# Flip it to false and all of it leaves." Enter
Sleep 800ms
Type "echo 'logcheck = false' > llmtpl.conf" Enter
Sleep 500ms
Type "llmtpl apply" Enter
Sleep 2s
Type "tree -a --noreport .claude" Enter
Sleep 4s
