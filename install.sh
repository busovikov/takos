#!/bin/sh
# Installs takos from source: a single static Go binary, no runtime
# dependencies of its own. The only hard requirement here is the Go
# toolchain (there is no release pipeline yet, so "install" means "build").
#
# Agent CLIs (claude-code, opencode, …) and ollama are optional — takos
# runs fine without them, but headless/interactive sessions need at least
# one agent CLI, and local models need ollama. This script detects both,
# prints their status and an install hint, and never fails because of them.
#
# Usage:
#   ./install.sh                 # installs to ~/.local/bin
#   PREFIX=/usr/local/bin ./install.sh

set -eu

PREFIX="${PREFIX:-$HOME/.local/bin}"
REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"

info()  { printf '  %s\n' "$1"; }
ok()    { printf '  \033[32m✔\033[0m %s\n' "$1"; }
warn()  { printf '  \033[33m⚠\033[0m %s\n' "$1"; }
die()   { printf '\033[31m✗ %s\033[0m\n' "$1" >&2; exit 1; }

command -v go >/dev/null 2>&1 || die "Go is not installed — see https://go.dev/dl/ (takos builds from source, no prebuilt release yet)"

echo "building takos..."
mkdir -p "$PREFIX"
version="$(cd "$REPO_ROOT" && git describe --tags --always --dirty 2>/dev/null || echo dev)"
( cd "$REPO_ROOT" && go build -ldflags "-X github.com/busovikov/takos/internal/version.Version=$version" \
    -o "$PREFIX/takos" ./cmd/takos )
ok "installed $PREFIX/takos ($version)"

case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *) warn "$PREFIX is not on your PATH — add it, e.g. export PATH=\"$PREFIX:\$PATH\"" ;;
esac

echo
echo "optional dependencies:"
if command -v ollama >/dev/null 2>&1; then
  ok "ollama: $(ollama --version 2>/dev/null | head -1)"
else
  warn "ollama not found — local models via \`ollama launch\` (brew install ollama, or see https://ollama.com)"
fi

found_agent=0
for pair in "claude:npm i -g @anthropic-ai/claude-code" "opencode:npm i -g opencode-ai" \
            "codex:npm i -g @openai/codex" "gemini:npm i -g @google/gemini-cli"; do
  bin="${pair%%:*}"; hint="${pair#*:}"
  if command -v "$bin" >/dev/null 2>&1; then
    ok "$bin found"
    found_agent=1
  else
    warn "$bin not found — $hint"
  fi
done
[ "$found_agent" = 1 ] || warn "no agent CLI found — sessions (takos task agent) need at least one"

echo
info "next: takos task init && takos doctor"
