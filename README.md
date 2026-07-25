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
  webui/          the built web UI, embedded into the binary (go:embed)
  version/
web/              web UI source (React/TS/Vite)
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

## Quick start

```
takos task init                 # creates ./.takos (workspace + workflow.yaml)
takos task create "Fix the bug" # T-0001, lands in the backlog
takos task list                 # interactive board in a TTY (--plain for a table)
takos doctor                    # checks the install, agent CLIs, workflow.yaml roles
```

`takos task init` creates the workspace in a hidden `.takos/` under the
current directory (or under `--workspace DIR` / `$TAKOS_WORKSPACE`) — the
project directory itself stays clean, the same way `git init` only adds
`.git`, and doubles as the default repo for tasks created here. Other
commands find the workspace by walking up from the current directory
looking for `.takos/`. Outside every workspace nothing is guessed: `takos
task list` opens the workspace picker (most recently used first), other
commands print the same list and take `--workspace NAME`. See `takos task
--help` and `takos --help` for the full command tree.

## Container

```
docker build -t takos .
docker run -v takos-data:/data -e OLLAMA_HOST=host.docker.internal:11434 takos
```

The image ships only the takos binary; bring your own agent CLI on top
(`RUN npm i -g @anthropic-ai/claude-code` in a derived image, or a bind
mount). ollama stays on the host — see the Dockerfile's comment.

## Web UI

The tracker daemon serves a React UI on its own port — the same
projections the TUI draws (staircase board, task card, review carousel),
read-first, with mutations going through the same validator as the CLI.

```
takos daemon start          # http://127.0.0.1:7434
```

Live updates arrive over per-workspace SSE (`/api/ws/{name}/events`): the
event carries no payload, it just means "re-fetch". The workspace
switcher reads `/api/workspaces` and remembers the choice in
localStorage.

Working on the UI:

```
pnpm --dir web install
pnpm --dir web dev          # :5173, proxies /api to a running daemon
pnpm --dir web build        # → internal/webui/dist, embedded on the next go build
```

`internal/webui/dist` is committed so `go build` never needs node; rebuild
and commit it when the UI changes.

## Development

```
go build ./...        # build
go test ./...         # tests
go vet ./...          # lint
go run ./cmd/takos --version
```
