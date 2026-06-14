# claude-migrate

Move a Claude Code project to a new path and keep its session history working. After the move, `claude --continue` resumes in the new location exactly as before.

## Install

```
go install github.com/mahendrakalkura/claude-migrate@latest
```

This puts the `claude-migrate` binary in your Go bin (`$(go env GOPATH)/bin`); make sure that is on your `PATH`.

## Usage

```
claude-migrate <src> <dst>
```

Close all Claude sessions first. The tool prints a preview and waits for confirmation:

```
claude-migrate

  project         /home/you/projects/foo  ->  /home/you/work/foo
  session folder  -home-you-projects-foo  ->  -home-you-work-foo
  transcripts     12 files
  history.jsonl   34 lines

Proceed? [y/N]
```

Only `y` proceeds. Anything else cancels. There are no flags.

## What it does

1. Copies the project folder to the new path.
2. Copies its session folder (transcripts, `memory/`, everything) to the encoded new name under the Claude config dir.
3. Rewrites the `project` field in `history.jsonl` on lines that point at the old path.
4. Deletes the originals.

The copies keep the originals until the last step, so a failure before then leaves the source untouched.

## Details

- **Config dir.** Reads `CLAUDE_CONFIG_DIR`, falling back to `~/.claude` - the same resolution Claude Code uses, so it targets whatever dir your Claude actually uses.
- **Path handling.** Resolves `<src>` to an absolute, symlink-resolved path (matching what Claude stores), and encodes paths with Claude's own rule: every non-alphanumeric character becomes `-`.
- **Faithful copy.** Byte-identical contents, preserved mode bits and mtimes, symlinks copied as symlinks. Nothing is filtered.
- **history.jsonl.** Only the top-level `project` field is changed, and only on lines that exactly equal the old path. Every other line is written back byte-for-byte. The file is rewritten to a temp file and atomically renamed.
- **Lock guard.** Refuses to run while any process named `claude` is running.

## Preconditions

The run aborts with one error line if any of these fail:

- source project directory exists
- source session folder exists
- destination project directory does not exist
- destination session folder does not exist
- `src` and `dst` differ, and neither is nested inside the other
- `dst`'s parent directory exists

## Build

```
make build   # builds ./claude-migrate
make lint    # gofmt, go vet, golangci-lint
```
