package runnerd

// opencodeAdapter drives OpenCode headless via `opencode run`. OpenCode
// mints its own session ids, so the deterministic UUID only names the
// session on the takos side; resume maps to --continue (the directory's
// last session) — a documented MVP limitation.
type opencodeAdapter struct{}

func (opencodeAdapter) ID() string { return "opencode" }

func (opencodeAdapter) Argv(r Request) []string {
	argv := []string{r.Bin, "run"}
	if r.Resume {
		argv = append(argv, "--continue")
	}
	if r.Model != "" {
		// "ollama/gemma4:e4b" is already opencode's provider/model format.
		argv = append(argv, "--model", r.Model)
	}
	return append(argv, r.Prompt)
}

func (opencodeAdapter) Classify(exitErr error, transcript string) string {
	return classify(exitErr, transcript, "usage limit", "rate limit", "quota")
}
