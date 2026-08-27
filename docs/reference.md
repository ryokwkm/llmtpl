# Reference

**English** | [日本語](reference.ja.md)

The [README](../README.md) covers getting started. This covers the details of the spec, and why the
spec is the way it is.

## Bundle layout

Overlay scanning goes exactly two levels deep: the bundle root, and directly inside any
dot-prefixed directory.

**A directory placed directly in a bundle is an error** (`<flag>/docs/`, say). Symlink cleanup walks
layer → container → entry, so as long as layers are limited to dot-prefixed names, its reach stays
inside `<target>/.<something>/`. Allow one at the bundle root and the search range for deletion
becomes the project's entire top level. Only file fragments may sit at the bundle root.

## Target detection

The only mark of a target is having an `llmtpl.conf`. The base `*.tmpl` files are a target's
**contents**, not its mark. Treat both as marks and `<P>` and `<P>/.claude` become targets at the
same time (the first counting `.claude/*.tmpl`, the second counting its own `*.tmpl`), and the
second — which has no `llmtpl.conf` — overwrites the generated file with thin content, every flag
off. Keeping the mark to one thing means this cannot happen.

## Slots — only when you want to choose where a fragment lands

When you want to choose where a fragment goes, write `{{- slot "<flag name>"}}` in the base `*.tmpl`.
**Slot name = flag name = bundle directory name**, and the fragment side declares nothing. A slot
naming a flag that does not exist is an error (that is how typos are caught).

Write no slot and the fragment is **appended to the end** of the generated file (in ascending bundle
order if there are several). Appending and going through a slot produce identical bytes, so choosing
a position later produces no diff.

## Search and bundle root resolution

**The search covers the given directory and everything under it.** There may be several targets if
you keep projects inside a config repository; two or more prints a count. Three guards stop it from
running away: `llm-tpl/`, `partials/`, `.archive/`, `node_modules`, `vendor`, `testdata`, and
`examples` are never looked at; **dot-prefixed directories are neither targets nor descended into**.
A separate repository (a directory with a `.git`) **is descended into** — the presence of
`llmtpl.conf` is itself the opt-in, so a repository without one is never dragged in. Running from a
parent directory of several repositories handles all their targets at once.

The bundle root resolves in this order: `--tpl-home` → `$LLMTPL_HOME` → **`bundle_root` in
`llmtpl.conf`** → walking up for `llm-tpl/` → `${XDG_CONFIG_HOME:-~/.config}/llmtpl`.
**Resolution is per target** — each target reads its own conf and walks up from its own directory —
and `apply` / `check` / `status` print one "bundle root:" line per root. As long as you
keep `llm-tpl/` next to your projects inside a config repository, only the fourth applies and you
write nothing.

### bundle_root

Write `bundle_root` when there is no bundle directory anywhere above you — that is, when you borrow
bundles from another repository.

```ini
# <target>/llmtpl.conf
bundle_root = ../../shared-bundles     # relative to the directory holding this conf; ~ works too
wiki = true
```

- The value is a path. **Unlike a flag it cannot be `true` / `false`** (the branch is on the key
  name, so typo detection on the flag side keeps working as before)
- A relative path is resolved against **the directory holding that conf**, so the result does not
  change with where you run from. `~` is expanded; `$VAR` is not. Symlinks are not resolved
- If the path does not exist, it **stops with an error** rather than falling through to the walk-up
  (falling through would turn a typo into "generation succeeded against a different bundle root")
- **It cannot be written in `defaults.conf`.** That file lives inside the bundle root, so the root is
  already resolved by the time it is read
- **It affects only the target whose conf names it.** Different targets may name different values;
  each is composed from its own root (they never conflict within one run)
- A bundle root directory cannot itself be named `bundle_root` (you could no longer turn on a flag by
  that name in a conf, so `apply` refuses)

⚠️ **A conf using this key cannot be read by an older llmtpl** (`bundle_root` fails to parse as an
unknown value). If you share confs across machines or repositories, update everywhere before writing it.

### Reading `status`

`status` prints a matrix of effective values, one row per target and one column per flag. Measured
from the README demo plus a `review` bundle and a `tools/.claude`:

```console
$ llmtpl status
2 targets
bundle root: /path/to/demo/llm-tpl (found by walking up)
target  hello  review
proj    ON     -
tools   ON     ON
```

## How composition works

**Markdown (`*.md.tmpl`)** — fragments at the same relative path in every ON bundle are collected and
inserted at a slot or at the end. Line 1 of the generated file carries `<!-- GENERATED ... -->` (an
HTML comment, so it costs no tokens in Claude Code's context).

**JSON (`*.json.tmpl`)** — fragments from ON bundles are deep-merged, and the target's own file is
layered last (the more specific wins). Bundles apply in name order. Objects merge recursively, arrays
union, scalars take the later value.

**Directories** — `.claude/rules` is **folded into a single directory** at `.claude/rules/<flag>`;
everything else (`skills`, `agents`, `commands`, `hooks`) is symlinked **entry by entry**. The
asymmetry comes from Claude Code: `.claude/rules/` is searched recursively for `.md`, so it can be
folded, while skills are discovered as `<name>/SKILL.md` and cannot take an extra level. Folding has a
side benefit — names inside `rules/<flag>/` are yours to choose.

Ownership of a link is decided by **whether the link points inside the bundle root**. That is why
anything belonging to the target itself (a hand-written `rules/foo.md`, say) survives being turned off.

## Template syntax

**Everything inside a `.tmpl` is evaluated as a Go template.** A line containing `{{ }}` — documenting
GitHub Actions' `${{ }}`, for instance — stops with a parse error. Nothing is silently dropped, and
`--dry-run` shows it. To keep a literal `{{`, write `{{"{{"}}`.

| Syntax | Meaning |
|---|---|
| `{{if .wiki}} … {{else}} … {{end}}` | flag condition (nesting = AND) |
| `{{- slot "wiki"}}` | a slot; put it on a line of its own (`{{-` eats the preceding newline) |
| `{{template "part.tmpl" .}}` | call a partial from `partials/*.tmpl` (do not forget the `.`) |
| `{{/* comment */}}` | absent from the generated file. `<!-- -->` **does appear**, so do not use it |

Flags arrive as **the set of every bundle name**, so `{{if}}` can reference a bundle that no conf
mentions (its value is false). Only a name that does not exist at all is an error.

**The leading newline of a fragment is the author's decision.** A fragment starting with `## Heading`
should carry one blank line at the front (without it the heading butts against the preceding bullet
list). A fragment meant to continue the previous list should not. The engine normalizes trailing
newlines only; it never touches the front.

## Living with it

### Interactive mode

`llmtpl` with no arguments puts **every target** below the current directory into one form, one
section per target (title + bundle root + checkboxes), then writes each target's `llmtpl.conf` and
**applies every target it showed** (same meaning as `llmtpl apply`: what you saw is what gets
applied). Targets with different roots each show their own shelf's flags.

**It only starts when stdin and stdout are both terminals.** Behind a pipe, a redirect, or in CI it
prints the help and exits 0, exactly as it did before the mode existed — a form waiting for input
that never comes would hang a script.

**Aborting (Esc or Ctrl-C) exits 130** and changes nothing. huh binds only Ctrl-C by default, so Esc
is added; filtering is turned off to free it (huh uses Esc to set and clear the filter).

**Each row is truncated to one line.** Descriptions from `bundle.conf` are often long; a row that
wrapped would take more screen lines than the list reckons with, pushing the first bundle off the
top. Only the description is shortened — the flag name and the default-on note always stay. Widen
the terminal to read more of it, or run `llmtpl bundles` for the full text.

**The list runs on the normal screen**, like any ordinary CLI. Each section carries its bundle
root in its heading, and the apply output (one root line per root, one report per target) remains in
the scrollback afterwards. ⚠️ More rows than the terminal holds still makes huh scroll, **with no
scrollbar**. Press ↑ if the count looks wrong (`llmtpl bundles` prints all of them).

**Only when there is no `llmtpl.conf` anywhere below** does it say so and ask whether to create one
in the current directory (if even one exists it simply shows them — there is no path that grows a
conf at a parent level). Nothing is written until the final confirmation, so aborting midway leaves
no file behind. Answering yes always produces the file even when every flag stays at its default,
because the presence of `llmtpl.conf` is what makes a directory a target.

The conf is edited in place, never regenerated, under four rules:

| The flag | What happens |
|---|---|
| has a line, already the wanted value | untouched |
| has a line with the other value | **only the value characters** are replaced, so alignment, indentation, and trailing whitespace survive |
| has no line, and the default already gives the wanted value | nothing is written |
| has no line, and the default gives the other value | `name = true\|false` is appended |

**No line is ever deleted.** An explicit `false` and a missing line mean the same thing to the
parser, but removing the line would strand the comment above it. For the same reason a duplicate key
has only its **last** line rewritten (the parser takes the last one, so that is the one that
decides), and `bundle_root` is never treated as a flag.

Writing less than you could is deliberate: every line in `llmtpl.conf` is one more place a later
change to `defaults.conf` fails to reach.

### CLI language

Every CLI message is bilingual. The language is picked from the first non-empty of
`LLMTPL_LANG` > `LC_ALL` > `LC_MESSAGES` > `LANG`: a value starting with `ja` selects Japanese,
anything else — including nothing set — selects English. `LLMTPL_LANG` overrides the language for
llmtpl alone (`LLMTPL_LANG=ja llmtpl apply`). Generated files are not affected: the `GENERATED`
header is fixed to English so it never varies by machine.

### Archiving

**An existing file is archived before being overwritten.** When llmtpl finds a file without its
generated marker, it copies it to `.archive/<label>.bk.<timestamp>` before writing. The archive lives
**directly under the target**, and the label is the path relative to the target (`.claude-CLAUDE.md`,
for example). JSON cannot carry a comment, so `.llmtpl-state.json` (a hash of what was generated)
decides "is this mine?" instead. Delete that file and an existing `settings.json` is archived as an
unknown file.

**Hand edits to a generated file are lost, not archived** (the marker identifies it as "what I wrote
last time"). Edit the `*.tmpl` source instead.

### The cost of generating into `~/.claude`

**Making `~/.claude/settings.json` a generated file has a price.** Claude Code's own writes
(`/model`, `/config`, `/plugin`, `/permissions`) are archived away by the next apply. Anything you
want to keep belongs in `settings.json.tmpl`. For the same reason, generating `~/.claude/CLAUDE.md`
means losing `#` quick memories at the next apply. **Generating `settings.json` is opt-in** (only
targets that place a `*.json.tmpl`), so the safe move is to leave it alone until you need it.

### Not guaranteed

1. A bundle fragment does not land unless the target has a `*.tmpl` at the same relative path
   (`apply` / `check` say so in one line, but it is not an error)
2. A symlink llmtpl did not create is deleted anyway if it points inside the bundle root
3. A failed link is not rolled back — the generated files stay
4. **Turning the same bundle on for several targets is not detected as double distribution** (the
   same skill can land in both user scope and project scope)
5. **When the bundle root lives in the same repository as the target, the source path in the
   `GENERATED` header is relative to the root** (so it starts outside the target). This holds whether
   you point at it with `--tpl-home`, `$LLMTPL_HOME`, or `bundle_root`

### Constraints

- Only `.md` and `.json` are generated (other extensions are skipped and reported)
- Generating `settings.local.json` is opt-in (only targets that place a `settings.local.json.tmpl`).
  Claude Code writes to it live via `/model` and `/permissions`, so making it a generated file means
  those writes trip drift detection at the next apply and land in `.archive/` (the declaration wins).
  Write anything you want to keep back into the `.tmpl`. Place no template and llmtpl leaves it alone
- `<repo>/.claude/CLAUDE.local.md` is not generated either (Claude Code does not read it there; place
  a `CLAUDE.local.md.tmpl` at the repository root and that becomes a target)
- `--mode copy` is not implemented (ownership is decided from the literal text of a symlink)
- Bundles cannot declare dependencies on each other (write fragments that do not depend on other flags)
