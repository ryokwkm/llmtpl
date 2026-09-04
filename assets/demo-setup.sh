#!/bin/sh
# assets/demo.tape が録画の前に走らせる下ごしらえ。README の "Try it" と同じツリーを作る。
# 引数は作業先ディレクトリ（空であること）。GIF に映らないので出力は捨ててよい。
#
# README 側の手順を写しているので、**README を直したらこちらも直す**。ここが食い違うと
# GIF と本文で別のものを見せることになる。
set -eu

DEST="${1:?usage: demo-setup.sh <dir>}"
mkdir -p "$DEST/llm-tpl/logcheck/.claude/rules" \
         "$DEST/llm-tpl/logcheck/.claude/hooks" \
         "$DEST/proj/.claude"
cd "$DEST"

# バンドル側 — 1. 指示（断片。拡張子 .tmpl が必須で、中身は素の Markdown）
cat > llm-tpl/logcheck/.claude/CLAUDE.md.tmpl <<'EOF'

## Work log checks

- Verify the format of `log/` when you finish a task. The rules are in `.claude/rules/logcheck/format.md`.
- To check by hand, run `.claude/hooks/verify.sh`.
EOF

# 2. ルールと 3. スクリプト（そのまま配られるので中身は何でもよい）
echo '- Every line starts with a timestamp.' > llm-tpl/logcheck/.claude/rules/format.md
printf '#!/bin/sh\necho ok\n' > llm-tpl/logcheck/.claude/hooks/verify.sh

# 4. hook の登録（settings.json へ deep merge される断片）
cat > llm-tpl/logcheck/.claude/settings.json.tmpl <<'EOF'
{
  "hooks": {
    "Stop": [
      { "hooks": [{ "type": "command", "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/verify.sh" }] }
    ]
  }
}
EOF

# ターゲット側 — conf 1 行と、proj 固有のものだけを持つ土台 2 枚
printf 'logcheck = false\n' > proj/llmtpl.conf
printf '# Instructions for proj\n\n- Write it in Go.\n' > proj/.claude/CLAUDE.md.tmpl
printf '{"model": "opus"}\n' > proj/.claude/settings.json.tmpl
