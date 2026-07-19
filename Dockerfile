# takos is a single static Go binary — the image only needs to build and
# ship it. Agent CLIs (claude-code, opencode, …) are not baked in: they
# need their own auth/runtime and are expected to be installed in the same
# container (or reached over the network) by whoever assembles the final
# image — see the second stage's comment.
#
# ollama stays on the HOST, never in this container: models are large,
# GPU access is host-level, and multiple takos containers should share one
# ollama. Point at it with OLLAMA_HOST (e.g. -e OLLAMA_HOST=host.docker.internal:11434).

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/busovikov/takos/internal/version.Version=${VERSION}" \
    -o /out/takos ./cmd/takos

# A minimal, non-scratch base: takos shells out to git (handover commits,
# `takos task list`'s repo filter) and to whichever agent CLI a role names,
# both of which need a real userland (ca-certificates, git, a shell) —
# scratch can't run either.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates git tzdata
COPY --from=build /out/takos /usr/local/bin/takos

# Bring your own agent CLI: e.g. `RUN npm i -g @anthropic-ai/claude-code`
# on top of this image, or bind-mount its binary in at runtime.

ENV TAKOS_HOME=/data/.takos
VOLUME ["/data"]
ENTRYPOINT ["takos"]
CMD ["daemon", "start", "--foreground"]
