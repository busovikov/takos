package runnerd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/session"
)

// historyIndex returns the position of the first history event matching
// actor and args prefix, or -1.
func historyIndex(events []core.HistoryEvent, actor, argsPrefix string) int {
	for i, e := range events {
		if e.Actor == actor && e.Verb == "session" && strings.HasPrefix(e.Args, argsPrefix) {
			return i
		}
	}
	return -1
}

// The second fresh session of a stage is preceded by the summarizer
// regenerating the digest; the first has nothing to condense.
func TestHandoffRegenBeforeSecondSession(t *testing.T) {
	mockAgent(t, "exit 0")
	ws, task := newRunnerWS(t)
	id := task.Meta.ID

	first, err := PlanJob(ws, id, JobOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Pre != nil {
		t.Fatalf("first planning session must have no digest pre-run: %+v", first.Pre)
	}
	if outcome, _ := first.Execute(context.Background()); outcome != session.OutcomeFinished {
		t.Fatalf("first run: %s", outcome)
	}

	second, err := PlanJob(ws, id, JobOpts{})
	if err != nil {
		t.Fatal(err)
	}
	pre := second.Pre
	if pre == nil {
		t.Fatal("second fresh session must regenerate the digest")
	}
	if pre.Name != id+"-planning-handoff#1" || pre.Role != session.SummarizerRole {
		t.Errorf("pre = %s (%s)", pre.Name, pre.Role)
	}
	if pre.Dir != task.Dir {
		t.Errorf("summarizer cwd = %s, want the task folder", pre.Dir)
	}
	argv := strings.Join(pre.Argv, " ")
	if !strings.Contains(argv, "--session-id") {
		t.Errorf("fresh digest run must use --session-id: %q", pre.Argv)
	}
	if !strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Errorf("summarizer writes the digest — auto permissions expected: %q", pre.Argv)
	}
	prompt := pre.Argv[len(pre.Argv)-1]
	if !strings.Contains(prompt, core.PlanHandOffFile) || !strings.Contains(prompt, "## Decisions") {
		t.Errorf("digest prompt misses target/template:\n%s", prompt)
	}

	if outcome, err := second.Execute(context.Background()); err != nil || outcome != session.OutcomeFinished {
		t.Fatalf("second run: %s, %v", outcome, err)
	}
	events, _ := core.ReadHistory(task.Dir)
	doneDigest := historyIndex(events, "agent:summarizer", "finished "+pre.Name)
	startMain := historyIndex(events, "agent:planner", "start "+id+"-planning#2")
	if doneDigest == -1 || startMain == -1 || doneDigest > startMain {
		t.Errorf("digest must finish before the session starts (digest=%d, session=%d)",
			doneDigest, startMain)
	}
}

// A digest run that hits its limit blocks the main session; the next entry
// resumes the same digest session.
func TestHandoffLimitBlocksMain(t *testing.T) {
	mockAgent(t, `case "$*" in *"Regenerate the hand-off digest"*) echo 'usage limit'; exit 1;; esac
exit 0`)
	ws, task := newRunnerWS(t)
	id := task.Meta.ID

	first, _ := PlanJob(ws, id, JobOpts{})
	first.Execute(context.Background())

	second, _ := PlanJob(ws, id, JobOpts{})
	outcome, err := second.Execute(context.Background())
	if outcome != session.OutcomeLimit || err == nil {
		t.Fatalf("limited digest must block the session: %s, %v", outcome, err)
	}
	events, _ := core.ReadHistory(task.Dir)
	if historyIndex(events, "agent:planner", "start "+id+"-planning#2") != -1 {
		t.Error("the main session started despite the digest limit")
	}

	third, err := PlanJob(ws, id, JobOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if third.Pre == nil || !third.Pre.Resume {
		t.Fatalf("after a digest limit the digest must resume: %+v", third.Pre)
	}
	if !strings.Contains(strings.Join(third.Pre.Argv, " "), "--resume") {
		t.Errorf("digest resume argv: %q", third.Pre.Argv)
	}
}

// hand-off.md is born on entering implementation: the stage scaffolds the
// file, and the very first executor session is preceded by the summarizer
// condensing the planning materials into it.
func TestHandoffSeedsFirstImplementationSession(t *testing.T) {
	mockAgent(t, "exit 0")
	ws, task := newRunnerWS(t)
	id := task.Meta.ID

	if err := core.SetCheck(task.Dir, "plan-resolved"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(id, "implementation", "user", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(task.Dir, core.HandOffFile)); err != nil {
		t.Errorf("hand-off.md not scaffolded on entering implementation: %v", err)
	}

	job, err := PlanJob(ws, id, JobOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if job.Role != "executor" || job.Pre == nil {
		t.Fatalf("first executor session must seed the digest: %+v", job)
	}
	if job.Pre.Name != id+"-implementation-handoff#1" {
		t.Errorf("pre name = %s", job.Pre.Name)
	}
	prompt := job.Pre.Argv[len(job.Pre.Argv)-1]
	if !strings.Contains(prompt, core.HandOffFile) || !strings.Contains(prompt, "## From review") {
		t.Errorf("digest prompt misses target/template:\n%s", prompt)
	}
}
