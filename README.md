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
  runnerd/        headless agent runner + daemon (:7433)
  rules/          automation engine: workflow.yaml rules, failure-path healing
  registry/       machine-local workspace list (~/.takos/workspaces.yaml)
  tui/            Bubble Tea: board, task card, review carousel
  version/
web/              web UI (React/TS, phase C)
```

## Install

```
./install.sh          # builds from source, installs to ~/.local/bin
                       # PREFIX=/usr/local/bin ./install.sh for a system install
```

Go is the only hard requirement (there is no release pipeline yet, so
"install" means "build"). Agent CLIs (claude-code, opencode, …) and ollama
are optional — the script detects them, prints a status line and an
install hint, and never fails because they're missing. `takos doctor`
re-runs the same checks (plus workflow.yaml role assignments) any time
after that.

## Container

```
docker build -t takos .
docker run -v takos-data:/data -e OLLAMA_HOST=host.docker.internal:11434 takos
```

The image ships only the takos binary; bring your own agent CLI on top
(`RUN npm i -g @anthropic-ai/claude-code` in a derived image, or a bind
mount). ollama stays on the host — see the Dockerfile's comment.

## Development

```
go build ./...        # build
go test ./...         # tests
go vet ./...          # lint
go run ./cmd/takos --version
```
