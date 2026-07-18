# Takos

A filesystem-native task tracker and coding-agent orchestrator. Everything is
plain files on disk: a task is a folder, its stage is the position of a
handler symlink inside the sprint folders, the journal is plain text. The
CLI/TUI merely organize work on top of the filesystem — everything can just
as well be done by hand with `mv`, `ls` and `cat`.

A single Go binary `takos`: CLI (cobra) + TUI (Bubble Tea) + the tracker and
agent-runner daemons. The only JavaScript is the web UI (`web/`, phase C).

## Layout

```
cmd/takos/        entry point
internal/
  cli/            cobra commands
  core/           tracker core: FS operations, transition validator, workflow
  fsstore/        atomic writes, locks, frontmatter
  agents/         detection of installed agent CLIs and their models
  trackerd/       tracker daemon: watcher, sprint scheduler, rules
  runnerd/        headless agent runner (phase B)
  tui/            Bubble Tea: board, task card, review carousel
  version/
web/              web UI (React/TS, phase C)
```

## Development

```
go build ./...        # build
go test ./...         # tests
go vet ./...          # lint
go run ./cmd/takos --version
```
