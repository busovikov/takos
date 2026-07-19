package runnerd

import "strings"

// claudeAdapter drives Claude Code headless: `claude -p` with the same
// --session-id/--resume contract the interactive sessions use.
type claudeAdapter struct{}

func (claudeAdapter) ID() string { return "claude-code" }

func (claudeAdapter) Argv(r Request) []string {
	argv := []string{r.Bin, "-p"}
	if r.Resume {
		argv = append(argv, "--resume", r.UUID)
	} else {
		argv = append(argv, "--session-id", r.UUID)
	}
	// ollama/* models are opencode territory; claude gets only its own.
	if r.Model != "" && !strings.HasPrefix(r.Model, "ollama/") {
		argv = append(argv, "--model", r.Model)
	}
	if r.PermissionMode == "auto" {
		// Headless has nobody to answer permission prompts; auto roles
		// (executor, handover) get the full-trust flag.
		argv = append(argv, "--dangerously-skip-permissions")
	}
	return append(argv, r.Prompt)
}

func (claudeAdapter) Classify(exitErr error, transcript string) string {
	return classify(exitErr, transcript, "usage limit", "rate limit", "limit reached")
}
