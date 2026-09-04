# Named blocks — several fragments from one bundle, each to a place you choose

**English** | [日本語](README.ja.md)

**This is an advanced escape hatch.** If the default (a `CLAUDE.md.tmpl` at the bundle root) is
enough, use that. The main README does not cover this.

## What it buys you

By default a bundle contributes **one** fragment to `CLAUDE.md` (`<bundle>/CLAUDE.md.tmpl`), and it
lands in one place (the slot `{{- slot "<bundle name>"}}`, or the end of the file if there is none).

When you want **several fragments from one bundle to land in different places** — the tone rules in
the development section, the vocabulary rules in the testing section — define named blocks under
`partials/` and call them from the target.

```
llm-tpl/styleguide/
  bundle.conf
  partials/blocks.tmpl      ← {{define "styleguide/tone"}} … {{end}}
proj/
  llmtpl.conf               ← styleguide = true
  .claude/CLAUDE.md.tmpl    ← {{- if .styleguide}}{{template "styleguide/tone" .}}{{end}}
```

**Prefix block names with the bundle name.** That keeps two bundles with `partials/` from colliding,
and is the closest thing to a namespace here.

This bundle has no `CLAUDE.md.tmpl`. A bundle that carries only `partials/` still exposes its blocks
to the target, as long as the flag is ON.

## Try it

```sh
cd examples/named-blocks
llmtpl apply --tpl-home ./llm-tpl proj
cat proj/.claude/CLAUDE.md          # → expected-on.md, plus the GENERATED marker on line 1

echo 'styleguide = false' > proj/llmtpl.conf
llmtpl apply --tpl-home ./llm-tpl proj
cat proj/.claude/CLAUDE.md          # → expected-off.md, plus the marker
```

## ⚠️ Always wrap the call in `{{if}}`

**This is the one trap, and it is the big one.**

Write `{{template "styleguide/tone" .}}` bare and turning the flag off makes `apply` **fail
outright** — not only for that target, but for every target after it.

```
llmtpl: cannot evaluate template (possibly a reference to an undefined flag): template: CLAUDE.md.tmpl:4:11: executing "CLAUDE.md.tmpl" at <{{template "styleguide/tone" .}}>: template "styleguide/tone" not defined
```

Block definitions are only loaded while the bundle is ON, and `{{template}}` is an error when the
definition is missing. A slot returns an empty string when nobody contributes, which is why it never
has this problem — **and why the slot is the default.**

Wrap it in `{{- if .styleguide}}` and the call is not evaluated at all when the flag is off, so it
disappears safely. That is how this example is written.

## When the bundle name has a hyphen

`{{if .auto-sync}}` is not valid in Go templates (`bad character U+002D`). Write
`{{if index . "auto-sync"}}` instead.

## Which one should you use?

| | Slot (the default) | Named blocks |
|---|---|---|
| Fragments one bundle can contribute | one | as many as you like |
| Where they land | one place (the slot, or the end) | anywhere you like |
| When the flag goes off | nothing happens | **forget the `{{if}}` and `apply` breaks** |
| What the target needs | one `{{- slot "name"}}` line (optional) | `{{if}}` + `{{template}}` |

**When in doubt, use the slot.** Come here once you actually have the requirement: one bundle's
content has to reach several places that are far apart.
