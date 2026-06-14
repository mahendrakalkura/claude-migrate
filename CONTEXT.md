# CONTEXT.md

Orientation for a coding agent working on this repo. Read this before changing anything.

## What this is

`claude-migrate` is a single-purpose Go CLI: it moves one Claude Code project from an old filesystem path to a new one and keeps that project's chat history resumable (`claude --continue` works after the move). It is a clean rewrite of an older bash tool called `clamp`. Scope is deliberately one operation - resist adding subcommands, flags, or modes.

## Files

- `main.go` - the entire program. One package, no internal packages.
- `README.md` - user-facing docs.
- `Makefile` - `make build`, `make lint`.
- `go.mod` - Go 1.26, standard library only. Do not add third-party dependencies without a strong reason.

## How a move works (main.go `run`)

1. Lock guard: abort if any process named exactly `claude` is running.
2. Resolve config dir, then canonicalize `<src>` and `<dst>`.
3. Check preconditions (see below).
4. Print a preview and require an interactive `y`.
5. Copy project folder src -> dst (originals kept).
6. Copy session folder old -> new under `<configdir>/projects/` (originals kept).
7. Rewrite the `project` field in `<configdir>/history.jsonl` for matching lines (atomic temp + rename).
8. Delete the originals.

If any step before 8 fails, `cleanup` removes whatever was copied and the original is untouched. There is intentionally no journal, no backup file, no rollback choreography beyond this - that simplicity is a deliberate decision, not an oversight. Do not re-add it.

## Non-obvious facts (verified from Claude Code's `cli.js`, do not "fix" these)

These were read directly from the bundled Claude Code source. They are the load-bearing correctness facts:

- **Config dir** (`uQ()`): `CLAUDE_CONFIG_DIR` env var if set, else `~/.claude`. Same resolution Claude uses, so the tool auto-targets the operator's real dir. Projects live in `<configdir>/projects`, the global index is `<configdir>/history.jsonl`.
- **Stored path** (`K0()` = `realpathSync(process.cwd())`): absolute, symlinks resolved, no trailing slash. The tool matches this with `filepath.Abs` then `filepath.EvalSymlinks` on `<src>`. The canonical string MUST match what Claude stored or the history match silently finds nothing.
- **Path encoding** (`aS3()` = `A.replace(/[^a-zA-Z0-9]/g,"-")`): every non-alphanumeric char becomes `-`, case preserved. This is how session folder names are derived. Verified stable across versions 2.0.55 - 2.1.177, and against 143/143 folders on disk.
- **Transcript `cwd` is not rewritten**: Claude's transcript loader (`QFA`) dispatches on `type`/`uuid`/`message`/`summary` and never reads the per-line `cwd`. Rewriting it would be cosmetic. The session folder NAME is what matters for resume, not transcript contents.

If a future Claude version changes any of these, this tool needs updating - that is why the encoding/normalization logic is centralized.

## history.jsonl rewrite (the only JSON-aware edit)

`history.jsonl` is one JSON object per line, every line with keys `display, pastedContents, timestamp, project, sessionId` in that order. Only the top-level `project` field is changed, and only on lines where it *exactly* equals the canonical src (not a substring - avoids prefix collisions like `/a/proj` vs `/a/proj2`, and avoids the `display` field, which holds user-typed prompts that may contain paths).

Mechanism (`rewriteLine`): unmarshal each line into a struct whose untouched fields are `json.RawMessage` (preserved byte-for-byte) and whose `project` is a `string`. Non-matching or unparseable lines are written verbatim. Matching lines are re-marshaled with `Encoder.SetEscapeHTML(false)` - Go's `encoding/json` HTML-escapes `<`, `>`, `&` by default, which would corrupt the ~thousand lines containing those characters. The whole file goes to a temp file and is atomically renamed.

## Preconditions (all fatal, checked before any mutation)

source dir exists; source session folder exists; dest dir does not exist; dest session folder does not exist; src != dst; dst not nested in src; src not nested in dst; dst's parent exists.

## Copy contract

Faithful: byte-identical contents, preserved mode bits and mtimes, symlinks copied as symlinks (not followed). No filtering. Special files (socket/fifo/device) cause an abort rather than an unfaithful copy. Directory mtimes are restored after their contents are written (a child write bumps the parent's mtime), see the `directoryTimes` map in `copyTree`.

## Build, lint, test

```
make build   # -> ./claude-migrate (gitignored)
make lint    # gofmt check, go vet, golangci-lint (must be 0 issues)
```

There are no automated tests, by design. House style for Go lives in the `go-style` skill: short declarations, no `_ =` error ignores, `fmt.Sprintf` over `+`, full variable names, alphabetical ordering. `golangci-lint`'s `errcheck` enforces the no-ignored-errors rule.

## Critical gotcha when testing

The lock guard refuses to run while any `claude` process exists. If you are an agent running *inside* a Claude session, the shipped binary will always abort at step 1 - you cannot exercise the migration path directly. To verify behavior, copy `main.go` to a temp dir, stub `claudeRunning` to `return false, nil`, build that, and run it against a throwaway `CLAUDE_CONFIG_DIR` sandbox. Never weaken the real lock guard to make testing easier.
