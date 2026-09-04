#!/usr/bin/env python3
"""README トップの Before / After 図（SVG）を英日 2 枚生成する。

    python3 assets/diagram.py

**手で SVG を編集しないこと。** 英日で構造が食い違うと、片方だけ古い図が README に載る。
文言を変えるときは下の TEXTS を直して再生成する。

色は 1 枚で完結させている（自前の暗い背景を持つ）。GitHub のライト / ダークどちらでも同じに
見せるためで、`prefers-color-scheme` は camo 経由の SVG では効かない。
同じ理由で CSS（`<style>`）を使わず、presentation attribute だけで書いている
（GitHub の SVG サニタイズで落ちる可能性を避ける）。

各セルを独立した `<text>` に分けて x を明示しているのは、閲覧者の環境のフォントに
依存せず桁を揃えるため。1 本の文字列にすると、CJK が等幅にならない環境で崩れる。
"""
import html

W, H = 1200, 430
BG, PANEL = "#0d1117", "#161b22"
DIM, TEXT, NUM = "#8b949e", "#d6dae0", "#79c0ff"
RED, GREEN, ARROW = "#ff9492", "#7ee787", "#484f58"
MONO = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
SANS = "-apple-system, BlinkMacSystemFont, Segoe UI, Helvetica, Arial, sans-serif"

L, R = 40, 648          # 左右パネルの原点
LN, LL = 250, 272       # 左パネルの番号列・ラベル列
RN = 916                # 右パネルの番号列
ROW0, STEP = 128, 26    # ツリー 1 行目の y と行送り

TEXTS = {
    "en": {
        "out": "assets/before-after.svg",
        "before_sub": "One feature = four files, repeated in every repository",
        "after_sub": "One feature = one directory",
        "labels": ["instructions", "rules", "script", "registration"],
        "same": "the same four",
        "before_foot": "12 files to put in by hand — and to take back out by hand",
        "after_foot": "3 lines. Flip one to false and that feature leaves, whole.",
    },
    "ja": {
        "out": "assets/before-after.ja.svg",
        "before_sub": "1 機能 = 4 ファイル。リポジトリごとに繰り返す",
        "after_sub": "1 機能 = 1 ディレクトリ",
        "labels": ["指示", "規約", "スクリプト", "登録"],
        "same": "同じ 4 つ",
        "before_foot": "12 ファイルを手で入れて、手で戻す",
        "after_foot": "conf の 3 行。1 行を false にすれば、その機能ごと出ていく。",
    },
}

BEFORE_TREE = ["proj-a/.claude/", "├── CLAUDE.md", "├── rules/format.md",
               "├── hooks/verify.sh", "└── settings.json"]
AFTER_TREE = ["llm-tpl/logcheck/.claude/", "├── CLAUDE.md.tmpl", "├── rules/format.md",
              "├── hooks/verify.sh", "└── settings.json.tmpl"]
CONFS = [("proj-a/llmtpl.conf", "logcheck = true"), ("proj-b/llmtpl.conf", "logcheck = true"),
         ("proj-c/llmtpl.conf", "logcheck = false")]


def build(t):
    o = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
         f'viewBox="0 0 {W} {H}" role="img">',
         f'<rect width="{W}" height="{H}" rx="10" fill="{BG}"/>',
         f'<rect x="20" y="20" width="568" height="{H-40}" rx="8" fill="{PANEL}"/>',
         f'<rect x="628" y="20" width="552" height="{H-40}" rx="8" fill="{PANEL}"/>']

    def txt(x, y, s, fill=TEXT, size=17, font=MONO, weight=None):
        w = f' font-weight="{weight}"' if weight else ""
        o.append(f'<text x="{x}" y="{y}" fill="{fill}" font-family="{font}" '
                 f'font-size="{size}"{w}>{html.escape(s)}</text>')

    # 見出し
    txt(L, 56, "BEFORE", RED, 19, SANS, "bold")
    txt(L, 84, t["before_sub"], DIM, 14, SANS)
    txt(R, 56, "AFTER", GREEN, 19, SANS, "bold")
    txt(R, 84, t["after_sub"], DIM, 14, SANS)

    # ツリー（番号とラベルは別セル）
    for i, line in enumerate(BEFORE_TREE):
        y = ROW0 + i * STEP
        txt(L, y, line)
        if i:
            txt(LN, y, str(i), NUM)
            txt(LL, y, t["labels"][i - 1], DIM, 15)
    for i, line in enumerate(AFTER_TREE):
        y = ROW0 + i * STEP
        txt(R, y, line)
        if i:
            txt(RN, y, str(i), NUM)

    # 繰り返されるリポジトリ / conf 1 行
    for i, name in enumerate(("proj-b/.claude/", "proj-c/.claude/")):
        y = ROW0 + (6 + i) * STEP
        txt(L, y, name)
        txt(LN, y, t["same"], DIM, 15)
    for i, (path, val) in enumerate(CONFS):
        y = ROW0 + (6 + i) * STEP
        txt(R, y, path)
        txt(RN - 60, y, val, GREEN if val.endswith("true") else DIM)

    # 矢印
    o.append(f'<path d="M596 212 h26 m-9 -9 l9 9 -9 9" fill="none" stroke="{ARROW}" '
             f'stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>')

    # まとめ
    foot = H - 40
    o.append(f'<line x1="{L}" y1="{foot-26}" x2="568" y2="{foot-26}" stroke="{ARROW}"/>')
    o.append(f'<line x1="{R}" y1="{foot-26}" x2="1160" y2="{foot-26}" stroke="{ARROW}"/>')
    txt(L, foot, t["before_foot"], RED, 15, SANS)
    txt(R, foot, t["after_foot"], GREEN, 15, SANS)

    o.append("</svg>")
    return "\n".join(o) + "\n"


for lang, t in TEXTS.items():
    open(t["out"], "w", encoding="utf-8").write(build(t))
    print("wrote", t["out"])
