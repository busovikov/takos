//go:build live

package session

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The live tier verifies the real claude CLI's session contract —
// --session-id creates a session, --resume continues it — with tokens from
// a local model via `ollama launch claude` (no API cost, no auth). Run:
//
//	go test -tags live ./internal/session -run TestLive -v
//
// TAKOS_LIVE_MODEL overrides the model (default gemma4:e4b). The test adds
// -p (print mode) on top of the contract flags: sessions must run
// non-interactively here; the flags under test are the same.
//
// The limit outcome cannot happen on a local model — that path stays with
// the mock tests.
func TestLiveClaudeSessionContract(t *testing.T) {
	for _, bin := range []string{"ollama", "claude"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not installed", bin)
		}
	}
	model := os.Getenv("TAKOS_LIVE_MODEL")
	if model == "" {
		model = "gemma4:e4b"
	}
	list, err := exec.Command("ollama", "list").Output()
	if err != nil || !strings.Contains(string(list), strings.Split(model, ":")[0]) {
		t.Skipf("ollama model %s is not pulled", model)
	}

	// The claude profile is isolated so `ollama launch` configuration and
	// the test sessions never touch the user's ~/.claude.
	configDir := t.TempDir()
	workDir := t.TempDir()
	uuid := UUID(Name("T-9999", "planning", 1))

	launch := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		full := append([]string{"launch", "claude", "--model", model, "-y", "--"}, args...)
		cmd := exec.CommandContext(ctx, "ollama", full...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// Fresh session with the deterministic UUID.
	out, err := launch("--session-id", uuid, "-p", "Reply with exactly: OK")
	if err != nil {
		t.Fatalf("--session-id run failed: %v\n%s", err, out)
	}

	// Resume must find the session in the store and answer again.
	out, err = launch("--resume", uuid, "-p", "Reply once more with exactly: OK")
	if err != nil {
		t.Fatalf("--resume run failed: %v\n%s", err, out)
	}
	t.Logf("resume output: %s", strings.TrimSpace(out))
}
