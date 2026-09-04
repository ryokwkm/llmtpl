# examples

**English** | [日本語](README.ja.md)

Ways of using llmtpl that the main README leaves out. **Every one of them is an advanced escape
hatch** — for the ordinary way, see the [README](../README.md).

Each directory runs on its own: pass `--tpl-home ./llm-tpl` to `apply`. The output is pinned in
`expected-*.md`, and `go test ./...` runs the real thing and compares against it, so these cannot go
stale.

| | What it buys you |
|---|---|
| [named-blocks](named-blocks/) | send several fragments from one bundle to places of the target's choosing |
