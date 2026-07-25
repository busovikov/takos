package rules

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/runnerd"
	"github.com/busovikov/takos/internal/session"
)

// mockClaudeScript is a fake claude CLI for the full-cycle E2E: it has no
// language model, only pattern matching on the (already {{…}}-expanded)
// prompt text to tell which role is talking, then performs that role's
// documented duty for real — writing task.md sections, plan.md, a review
// round, a git commit, a hand-off digest. Extracted facts persist in a
// per-uuid state file so a --resume call (whose argv carries no prompt)
// still knows what it is doing.
//
// MOCK_LIMIT_ONCE_FILE, when set and not yet present, turns exactly one
// invocation (whichever happens first after the caller creates the
// variable) into a usage-limit failure — the same failure path a real
// agent hits, exercised without any wall-clock waiting.
const mockClaudeScript = `#!/bin/bash
if [ "${1:-}" = --version ]; then echo "mock 1.0"; exit 0; fi

if [ -n "${MOCK_LIMIT_ONCE_FILE:-}" ] && [ ! -f "$MOCK_LIMIT_ONCE_FILE" ]; then
  : > "$MOCK_LIMIT_ONCE_FILE"
  echo "You have hit your usage limit."
  exit 1
fi

UUID="$3"
PROMPT="${@: -1}"
STATE="${MOCK_STATE_DIR:-/tmp}/$UUID.env"

extract() {
  printf '%s' "$PROMPT" | grep -oE "[^\"' ]*/tasks/T-[0-9]+-[a-z0-9-]+$1" | head -1
}

if [ -n "$PROMPT" ]; then
  TASKMD=""; PLANMD=""; ROUND=""; TARGET=""
  case "$PROMPT" in
    *"Study the repository"*)
      ROLE=context; TASKMD=$(extract '/task\.md') ;;
    *"You are planning task"*)
      ROLE=planner; PLANMD=$(extract '/plan\.md') ;;
    *"Implement task"*)
      ROLE=executor ;;
    *"Hand task"*"over to review"*)
      ROLE=handover; TASKMD=$(extract '/task\.md') ;;
    *"Review the changes of task"*)
      ROLE=reviewer; ROUND=$(extract '/reviews/R-[0-9]+\.md') ;;
    *"Regenerate the hand-off digest"*)
      ROLE=summarizer; TARGET=$(extract '/[a-zA-Z-]*hand-off\.md') ;;
    *)
      ROLE=generic ;;
  esac
  {
    echo "ROLE=$ROLE"
    echo "TASKMD=$TASKMD"
    echo "PLANMD=$PLANMD"
    echo "ROUND=$ROUND"
    echo "TARGET=$TARGET"
  } > "$STATE"
elif [ -f "$STATE" ]; then
  . "$STATE"
fi

case "${ROLE:-}" in
  context)
    [ -n "$TASKMD" ] && printf '\n## Context\nmock context notes\n' >> "$TASKMD"
    echo "context written" ;;
  planner)
    [ -n "$PLANMD" ] && printf '# Plan\n1. step one\n2. step two\n' > "$PLANMD"
    echo "plan written" ;;
  executor)
    echo "mock implementation" > mockchange.txt
    echo "implemented" ;;
  handover)
    git add -A >/dev/null 2>&1
    git -c user.email=mock@takos -c user.name="Mock Agent" commit -q -m "mock handover commit" >/dev/null 2>&1
    [ -n "$TASKMD" ] && printf '\n## What is done\nmock work done\n\n## Docs\nn/a\n\n## Testing\nn/a\n' >> "$TASKMD"
    echo "handed over" ;;
  reviewer)
    if [ -n "$ROUND" ]; then
      mkdir -p "$(dirname "$ROUND")"
      printf '# Review\n\n## [R1] Mock finding — open\nlooks fine, one nit\n' > "$ROUND"
    fi
    echo "reviewed" ;;
  summarizer)
    [ -n "$TARGET" ] && printf 'mock digest\n' > "$TARGET"
    echo "digest written" ;;
  *)
    echo "generic mock run" ;;
esac
exit 0
`

// installMockClaude puts the fake CLI on PATH.
func installMockClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(mockClaudeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// runGit runs one git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitLogCount(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Split(strings.TrimSpace(string(out)), "\n"))
}

// TestFullCycleE2E is the B-phase CI end-to-end: one task rides every role
// of the default workflow — context (auto), planner (driven directly here,
// though a rule would chain it too), the
// plan-resolved gate, executor (manual, hitting a usage limit on the way
// in and resuming past it), handover (auto, a real git commit in a scratch
// repo), reviewer (auto, chained off handover's finish) — and a human
// triage decision carries it to done. No daemon runs; the rules engine and
// runnerd execute in-process, so the whole thing is deterministic and fast.
func TestFullCycleE2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("TAKOS_HOME", t.TempDir()) // no runner daemon — in-process spawn
	t.Setenv("MOCK_STATE_DIR", t.TempDir())
	installMockClaude(t)

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-q")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "-c", "user.email=test@takos", "-c", "user.name=test", "commit", "-q", "-m", "init")
	commitsBefore := gitLogCount(t, repoDir)

	ws, err := core.Init(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := ws.CreateTask("E2E full cycle", core.CreateOpts{Repo: repoDir})
	if err != nil {
		t.Fatal(err)
	}
	id := task.Meta.ID
	e := New(ws, "e2e", nil, nil)
	ctx := context.Background()

	// refinement -> planning: context-on-planning fires automatically.
	if _, err := ws.MoveTask(id, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	e.Scan(ctx)
	e.Wait()
	task = reload(t, task.Dir)
	if !strings.Contains(task.Body, "## Context") {
		t.Fatalf("context role did not write task.md:\n%s", task.Body)
	}

	// plan-after-context would auto-spawn the planner too, but only on a
	// later Scan — none happens between here and this manual entry, so it
	// stays a deliberate, direct job in this test.
	planJob, err := runnerd.PlanJob(ws, id, runnerd.JobOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if planJob.Role != "planner" {
		t.Fatalf("role = %s, want planner", planJob.Role)
	}
	if outcome, err := planJob.Execute(ctx); err != nil || outcome != session.OutcomeFinished {
		t.Fatalf("planner run: %s, %v", outcome, err)
	}
	planPath := filepath.Join(task.Dir, "plan.md")
	if data, err := os.ReadFile(planPath); err != nil || !strings.Contains(string(data), "step one") {
		t.Fatalf("plan.md: %q, %v", data, err)
	}

	// The plan-resolved gate: a human check, then the implementation move.
	if err := core.SetCheck(task.Dir, "plan-resolved"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(id, "implementation", "user", ""); err != nil {
		t.Fatal(err)
	}

	// The executor's first entry has no digest pre-run (nothing to condense
	// yet — it reads plan.md directly), so a usage limit on the way in hits
	// the main session itself; it must still resume cleanly on the next
	// entry.
	limitFlag := filepath.Join(t.TempDir(), "limit-once")
	t.Setenv("MOCK_LIMIT_ONCE_FILE", limitFlag)

	blocked, err := runnerd.PlanJob(ws, id, runnerd.JobOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Pre != nil {
		t.Fatalf("first executor entry must have no digest pre-run: %+v", blocked.Pre)
	}
	outcome, execErr := blocked.Execute(ctx)
	if outcome != session.OutcomeLimit || execErr != nil {
		t.Fatalf("first executor entry = %s, %v — want a blocked limit", outcome, execErr)
	}
	if _, err := os.ReadFile(filepath.Join(repoDir, "mockchange.txt")); err == nil {
		t.Fatal("the executor ran despite hitting a limit")
	}

	resumed, err := runnerd.PlanJob(ws, id, runnerd.JobOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := resumed.Execute(ctx); err != nil || outcome != session.OutcomeFinished {
		t.Fatalf("resumed executor entry: %s, %v", outcome, err)
	}
	if _, err := os.ReadFile(filepath.Join(repoDir, "mockchange.txt")); err != nil {
		t.Fatalf("executor did not run after resuming past the limit: %v", err)
	}

	// implementation -> review: handover commits in the scratch repo and
	// updates task.md; its finish chains the reviewer automatically.
	if _, err := ws.MoveTask(id, "review", "user", ""); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		e.Scan(ctx)
		e.Wait()
		events, _ := core.ReadHistory(task.Dir)
		return hasFinishedSession(events, "reviewer")
	})

	if got := gitLogCount(t, repoDir); got != commitsBefore+1 {
		t.Fatalf("repo commits = %d, want %d (handover must commit exactly once)", got, commitsBefore+1)
	}
	task = reload(t, task.Dir)
	for _, want := range []string{"## What is done", "## Docs", "## Testing"} {
		if !strings.Contains(task.Body, want) {
			t.Errorf("handover did not write %q into task.md", want)
		}
	}

	round, roundPath, err := core.LoadLatestRound(task.Dir)
	if err != nil || round == nil || len(round.Items) == 0 {
		t.Fatalf("no review round after the reviewer ran: %v", err)
	}
	if round.Items[0].Status != core.ReviewOpen {
		t.Fatalf("finding status = %s, want open (the reviewer only reports)", round.Items[0].Status)
	}

	// The triage decision belongs to a human: resolve the lone finding —
	// the round's verdict computes to done and moves the task there.
	if err := round.SetStatus(round.Items[0].ID, core.ReviewResolved); err != nil {
		t.Fatal(err)
	}
	if err := core.SaveRound(roundPath, round); err != nil {
		t.Fatal(err)
	}
	if v, done := round.Verdict(); !done || v != "done" {
		t.Fatalf("verdict = %s, %v, want done", v, done)
	}
	if _, err := ws.MoveTask(id, "done", "engine", ""); err != nil {
		t.Fatal(err)
	}

	pos, err := ws.FindHandler(id)
	if err != nil || pos.Stage != "done" {
		t.Fatalf("final stage = %v (%v), want done", pos, err)
	}

	// Every automated rule fired exactly once across the whole run.
	events, _ := core.ReadHistory(task.Dir)
	fired := map[string]int{}
	for _, ev := range events {
		if ev.Verb == "rule" {
			fired[strings.Fields(ev.Args)[0]]++
		}
	}
	for _, rule := range []string{"context-on-planning", "handover-on-review", "review-after-handover"} {
		if fired[rule] != 1 {
			t.Errorf("rule %s fired %d times, want 1", rule, fired[rule])
		}
	}
}

func reload(t *testing.T, taskDir string) *core.Task {
	t.Helper()
	task, err := core.LoadTask(taskDir)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func hasFinishedSession(events []core.HistoryEvent, role string) bool {
	for _, e := range events {
		if e.Actor == "agent:"+role && e.Verb == "session" && strings.HasPrefix(e.Args, "finished") {
			return true
		}
	}
	return false
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
