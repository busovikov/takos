// Package rules is the automation engine: it replays each task's history
// against the workflow.yaml rules and fires whatever has not been fired
// yet. The chronicle is also the ledger — a fired rule appends a "rule"
// record referencing its trigger event, which makes the engine stateless
// and replay-safe across daemon restarts: no cursor files, no double
// firing, missed events are picked up on the next scan.
package rules

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/runnerd"
	"github.com/busovikov/takos/internal/session"
)

// SpawnSpec is one role run requested by a rule.
type SpawnSpec struct {
	Role        string
	PromptExtra string
	// Session, when set, resumes this exact session by name instead of
	// planning a stage entry — the failure paths re-enter what they left.
	Session string
	// ClientToken is deterministic (workspace/task/rule/event), so a retry
	// after a tracker restart never starts a second job on the runner daemon.
	ClientToken string
}

// SpawnFunc starts a role run; tests inject theirs. The default prefers the
// runner daemon and falls back to executing in-process.
type SpawnFunc func(ctx context.Context, taskID string, spec SpawnSpec) error

// Engine evaluates the rules of one workspace. Scan is called from a single
// goroutine (the instance loop); only the in-flight bookkeeping of async
// in-process jobs needs locking.
type Engine struct {
	ws     *core.Workspace
	wsName string // registry name — the runner daemon addresses workspaces by it
	log    *log.Logger
	spawn  SpawnFunc

	mu       sync.Mutex
	inflight map[string]bool // task id → an in-process job is still starting/running
	wg       sync.WaitGroup
}

// New builds the engine; spawn == nil selects the default spawner.
func New(ws *core.Workspace, wsName string, lg *log.Logger, spawn SpawnFunc) *Engine {
	e := &Engine{ws: ws, wsName: wsName, log: lg, inflight: map[string]bool{}}
	e.spawn = spawn
	if e.spawn == nil {
		e.spawn = e.defaultSpawn
	}
	return e
}

// Wait blocks until every in-process job has ended (their ctx is expected
// to be cancelled by the caller first).
func (e *Engine) Wait() { e.wg.Wait() }

// Scan evaluates every rule against every active task (sprint stages only —
// backlog and archive are quiet) and returns human-readable action lines.
func (e *Engine) Scan(ctx context.Context) []string {
	handlers, err := e.ws.ListHandlers("")
	if err != nil {
		return []string{fmt.Sprintf("rules: %v", err)}
	}
	active, activeErr := e.ws.ActiveSprint()
	var actions []string
	seen := map[string]bool{}
	for _, h := range handlers {
		if h.Area == core.AreaBacklog || h.Area == core.AreaArchive {
			continue
		}
		if h.Area == core.AreaSprint && (activeErr != nil || h.Sprint != active.Name) {
			continue
		}
		name := filepath.Base(h.Path)
		if strings.HasPrefix(name, ".") {
			continue
		}
		id := core.HandlerID(name)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		actions = append(actions, e.scanTask(ctx, id, h.Stage)...)
	}
	return actions
}

// scanTask fires the pending rules of one task.
func (e *Engine) scanTask(ctx context.Context, id, curStage string) []string {
	taskDir, err := e.ws.FindTaskDir(id)
	if err != nil {
		return nil
	}
	task, err := core.LoadTask(taskDir)
	if err != nil {
		return nil
	}
	if task.Meta.Automation == "paused" {
		return nil
	}
	// A session whose process died without a terminal record (the daemon was
	// killed mid-job) is finalized here — its interrupted event is what the
	// retry path below reacts to.
	session.RepairStale(taskDir)
	events, err := core.ReadHistory(taskDir)
	if err != nil {
		return nil
	}

	handled := lastHandled(events)
	budget := e.ws.WF.Settings.RuleBudgetPerTaskHour
	if budget <= 0 {
		budget = 20
	}
	spent := firedSince(events, time.Now().Add(-time.Hour))

	var actions []string
	for _, r := range e.ws.WF.Rules {
		tr, err := latestTrigger(r, events, curStage)
		if err != nil {
			// A broken `if` never resolves — claim the latest event once.
			if tr != nil && tr.idx > handledIdx(handled, r.ID) {
				core.AppendHistory(taskDir, "engine", "rule-error",
					fmt.Sprintf("%s #%d %v", r.ID, tr.idx, err))
				actions = append(actions, fmt.Sprintf("%s: rule %s: %v", id, r.ID, err))
			}
			continue
		}
		if tr == nil || tr.idx <= handledIdx(handled, r.ID) {
			continue
		}
		if spent >= budget {
			actions = append(actions,
				fmt.Sprintf("%s: rule %s deferred — budget %d/hour exhausted", id, r.ID, budget))
			continue
		}
		line, fired := e.fire(ctx, id, taskDir, r, tr)
		if line != "" {
			actions = append(actions, id+": "+line)
		}
		if fired {
			spent++
		}
	}
	if line := e.failurePath(ctx, id, taskDir, curStage, task, events, handled, spent, budget); line != "" {
		actions = append(actions, id+": "+line)
	}
	return actions
}

// Built-in failure rules; their ledger ids are reserved.
const (
	autoResumeRule = "auto-resume-limit"
	autoRetryRule  = "auto-retry-interrupted"
)

// failurePath heals the task's last headless session: a limit verdict is
// resumed once the pause lapses (limits reset with time), a crash is
// resumed up to sessionMaxRetries times, and past that the task's
// automation pauses — a broken agent must not loop. Sessions launched by
// the user are left alone: the entry policy consults them at the keyboard.
func (e *Engine) failurePath(ctx context.Context, id, taskDir, curStage string,
	task *core.Task, events []core.HistoryEvent, handled map[string]int, spent, budget int) string {

	idx, outcome, name := lastTerminalSession(events)
	if idx < 0 || outcome == session.OutcomeFinished {
		return ""
	}
	launcher := launcherOf(events, idx, name)
	if !strings.HasPrefix(launcher, "agent:") {
		return ""
	}
	role := strings.TrimPrefix(launcher, "agent:")
	// A stale session (the task has moved on) is not resumed; moving back
	// makes it eligible again. Digests belong to their base stage.
	stage := session.StageOf(id, name)
	if strings.TrimSuffix(stage, "-handoff") != curStage {
		return ""
	}

	ruleID, gate := autoResumeRule, time.Duration(defaulted(e.ws.WF.Settings.LimitRetryMinutes, 15))*time.Minute
	if outcome == session.OutcomeInterrupted {
		ruleID, gate = autoRetryRule, time.Duration(defaulted(e.ws.WF.Settings.SessionRetryDelaySec, 30))*time.Second
	}
	if idx <= handledIdx(handled, ruleID) {
		return ""
	}
	if time.Since(events[idx].At) < gate {
		return "" // the pause; a later poll tick retries
	}
	if spent >= budget {
		return fmt.Sprintf("rule %s deferred — budget %d/hour exhausted", ruleID, budget)
	}

	// Crash retries are bounded; exhausting them pauses the automation.
	if outcome == session.OutcomeInterrupted {
		maxRetries := defaulted(e.ws.WF.Settings.SessionMaxRetries, 2)
		if resumeCount(events, name) >= maxRetries {
			task.Meta.Automation = "paused"
			if err := task.Save(); err != nil {
				return fmt.Sprintf("rule %s: pausing automation: %v", ruleID, err)
			}
			core.AppendHistory(taskDir, "engine", "notify",
				fmt.Sprintf("automation paused: session %s keeps dying (%d resumes spent)", name, maxRetries))
			core.AppendHistory(taskDir, "engine", "rule",
				fmt.Sprintf("%s #%d paused automation", ruleID, idx))
			return fmt.Sprintf("rule %s → automation paused (%s)", ruleID, name)
		}
	}

	e.mu.Lock()
	busy := e.inflight[id]
	e.mu.Unlock()
	if busy || session.Active(taskDir) != nil {
		return ""
	}
	spec := SpawnSpec{
		Role: role, Session: name,
		ClientToken: fmt.Sprintf("rule:%s:%s:%s:%d", e.wsName, id, ruleID, idx),
	}
	if err := e.spawn(ctx, id, spec); err != nil {
		if strings.Contains(err.Error(), "already running") {
			return ""
		}
		core.AppendHistory(taskDir, "engine", "rule-error",
			fmt.Sprintf("%s #%d resume %s: %v", ruleID, idx, name, err))
		return fmt.Sprintf("rule %s: resume %s: %v", ruleID, name, err)
	}
	core.AppendHistory(taskDir, "engine", "rule",
		fmt.Sprintf("%s #%d resume %s", ruleID, idx, name))
	return fmt.Sprintf("rule %s → resume %s", ruleID, name)
}

// lastTerminalSession finds the newest session ending — the task's session
// health is whatever ended last.
func lastTerminalSession(events []core.HistoryEvent) (idx int, outcome, name string) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Verb != "session" {
			continue
		}
		f := strings.Fields(events[i].Args)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case session.OutcomeFinished, session.OutcomeLimit, session.OutcomeInterrupted:
			return i, f[0], f[1]
		}
	}
	return -1, "", ""
}

// launcherOf names the actor that last started/resumed the session — the
// terminal record may come from the repair pass ("engine"), the launcher
// tells whose session it really is.
func launcherOf(events []core.HistoryEvent, before int, name string) string {
	for i := before; i >= 0; i-- {
		if events[i].Verb != "session" {
			continue
		}
		f := strings.Fields(events[i].Args)
		if len(f) >= 2 && f[1] == name && (f[0] == "start" || f[0] == "resume") {
			return events[i].Actor
		}
	}
	return ""
}

// resumeCount counts the resumes a session has already consumed.
func resumeCount(events []core.HistoryEvent, name string) int {
	n := 0
	for _, ev := range events {
		if ev.Verb != "session" {
			continue
		}
		f := strings.Fields(ev.Args)
		if len(f) >= 2 && f[0] == "resume" && f[1] == name {
			n++
		}
	}
	return n
}

func defaulted(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// fire runs a rule's actions for one trigger. The "rule" ledger record is
// appended after the synchronous parts succeed; a busy task (live session)
// defers the whole rule to a later scan — no record, natural retry.
func (e *Engine) fire(ctx context.Context, id, taskDir string, r core.Rule, tr *trigger) (string, bool) {
	acts := r.Then
	if r.Spawn != nil {
		acts = append(acts, core.Action{Spawn: r.Spawn})
	}

	// A spawn needs the task's session slot: defer while one is live (or an
	// in-process job of ours has not written its marker yet).
	for _, a := range acts {
		if a.Spawn == nil {
			continue
		}
		e.mu.Lock()
		busy := e.inflight[id]
		e.mu.Unlock()
		if busy || session.Active(taskDir) != nil {
			return "", false
		}
	}

	var summary []string
	for _, a := range acts {
		switch {
		case a.Spawn != nil:
			spec := SpawnSpec{
				Role:        a.Spawn.Role,
				PromptExtra: a.Spawn.PromptExtra,
				ClientToken: fmt.Sprintf("rule:%s:%s:%s:%d", e.wsName, id, r.ID, tr.idx),
			}
			if err := e.spawn(ctx, id, spec); err != nil {
				if strings.Contains(err.Error(), "already running") {
					return "", false // lost the race for the slot — retry later
				}
				core.AppendHistory(taskDir, "engine", "rule-error",
					fmt.Sprintf("%s #%d spawn %s: %v", r.ID, tr.idx, a.Spawn.Role, err))
				return fmt.Sprintf("rule %s: spawn %s: %v", r.ID, a.Spawn.Role, err), true
			}
			summary = append(summary, "spawn "+a.Spawn.Role)
		case a.Move != "":
			if _, err := e.ws.MoveTask(id, a.Move, "engine", ""); err != nil {
				core.AppendHistory(taskDir, "engine", "rule-error",
					fmt.Sprintf("%s #%d move %s: %v", r.ID, tr.idx, a.Move, err))
				return fmt.Sprintf("rule %s: move: %v", r.ID, err), true
			}
			summary = append(summary, "move "+a.Move)
		case a.Check != "":
			if err := core.SetCheck(taskDir, a.Check); err != nil {
				return fmt.Sprintf("rule %s: check: %v", r.ID, err), false
			}
			core.AppendHistory(taskDir, "engine", "check", a.Check)
			summary = append(summary, "check "+a.Check)
		case a.Notify != "":
			core.AppendHistory(taskDir, "engine", "notify", a.Notify)
			summary = append(summary, "notify")
		}
	}
	core.AppendHistory(taskDir, "engine", "rule",
		fmt.Sprintf("%s #%d %s", r.ID, tr.idx, strings.Join(summary, ", ")))
	return fmt.Sprintf("rule %s → %s", r.ID, strings.Join(summary, ", ")), true
}

// defaultSpawn prefers the runner daemon (idempotent by ClientToken); with
// no daemon around the job runs in-process — the tracker daemon is then the
// only process the automation needs.
func (e *Engine) defaultSpawn(ctx context.Context, taskID string, spec SpawnSpec) error {
	client, err := runnerd.Connect()
	if err == nil {
		_, err = client.Submit(e.wsName, taskID, runnerd.SubmitRequest{
			Role: spec.Role, PromptExtra: spec.PromptExtra,
			Session: spec.Session, ClientToken: spec.ClientToken,
		})
		return err
	}
	if !errors.Is(err, runnerd.ErrNotRunning) {
		return err
	}

	var job *runnerd.Job
	if spec.Session != "" {
		job, err = runnerd.ResumeJob(e.ws, taskID, spec.Session, spec.Role)
	} else {
		job, err = runnerd.PlanJob(e.ws, taskID, runnerd.JobOpts{
			Role: spec.Role, PromptExtra: spec.PromptExtra,
		})
	}
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.inflight[taskID] = true
	e.mu.Unlock()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() {
			e.mu.Lock()
			delete(e.inflight, taskID)
			e.mu.Unlock()
		}()
		if outcome, err := job.Execute(ctx); err != nil {
			e.logf("rules: job %s: %v", job.Name, err)
		} else {
			e.logf("rules: job %s %s", job.Name, outcome)
		}
	}()
	return nil
}

func (e *Engine) logf(format string, a ...any) {
	if e.log != nil {
		e.log.Printf(format, a...)
	}
}

// --- trigger extraction over history ---

type trigger struct {
	idx  int // index of the trigger event in the parsed history
	vars map[string]string
}

// latestTrigger returns the newest event matching the rule's `on` clause
// whose `if` condition holds (older matches collapse — firing the latest
// advances the ledger past them). A broken condition returns the newest
// match plus the error so the caller can claim it.
func latestTrigger(r core.Rule, events []core.HistoryEvent, curStage string) (*trigger, error) {
	for i := len(events) - 1; i >= 0; i-- {
		vars, ok := matchEvent(r.On, events[i], curStage)
		if !ok {
			continue
		}
		pass, err := Eval(r.If, vars)
		if err != nil {
			return &trigger{idx: i, vars: vars}, err
		}
		if pass {
			return &trigger{idx: i, vars: vars}, nil
		}
	}
	return nil, nil
}

// matchEvent tests one history event against an `on` clause and extracts
// the condition variables.
func matchEvent(on string, ev core.HistoryEvent, curStage string) (map[string]string, bool) {
	f := strings.Fields(on)
	switch {
	case len(f) == 2 && f[0] == "enter":
		// Fires only while the task is still in that stage: a stale enter
		// (the task has moved on) must not summon its automation back.
		if f[1] != curStage {
			return nil, false
		}
		switch ev.Verb {
		case "move":
			parts := strings.Split(ev.Args, "->")
			if len(parts) == 2 && strings.TrimSpace(parts[1]) == f[1] {
				return map[string]string{
					"stage": f[1], "from": strings.TrimSpace(parts[0]), "actor": ev.Actor,
				}, true
			}
		case "created":
			if fields := strings.Fields(ev.Args); len(fields) > 0 && fields[0] == f[1] {
				return map[string]string{"stage": f[1], "actor": ev.Actor}, true
			}
		}
	case len(f) == 1 && f[0] == "session-finished":
		if ev.Verb != "session" {
			return nil, false
		}
		fields := strings.Fields(ev.Args)
		if len(fields) < 2 {
			return nil, false
		}
		outcome := fields[0]
		if outcome != session.OutcomeFinished && outcome != session.OutcomeLimit &&
			outcome != session.OutcomeInterrupted {
			return nil, false
		}
		role := ""
		if strings.HasPrefix(ev.Actor, "agent:") {
			role = strings.TrimPrefix(ev.Actor, "agent:")
		}
		return map[string]string{
			"outcome": outcome, "session": fields[1], "role": role, "actor": ev.Actor,
		}, true
	case len(f) == 2 && f[0] == "check":
		if ev.Verb == "check" && strings.TrimSpace(ev.Args) == f[1] {
			return map[string]string{"check": f[1], "actor": ev.Actor}, true
		}
	case len(f) == 1 && f[0] == "review":
		// A triage decision: "R-001.md R2 -> resolved".
		if ev.Verb == "triage" {
			fields := strings.Fields(ev.Args)
			vars := map[string]string{"actor": ev.Actor}
			if len(fields) > 0 {
				vars["status"] = fields[len(fields)-1]
			}
			return vars, true
		}
	}
	return nil, false
}

// --- the ledger ---

// lastHandled maps rule id → the highest trigger index already claimed by a
// "rule"/"rule-error" record.
func lastHandled(events []core.HistoryEvent) map[string]int {
	out := map[string]int{}
	for _, ev := range events {
		if ev.Verb != "rule" && ev.Verb != "rule-error" {
			continue
		}
		f := strings.Fields(ev.Args)
		if len(f) < 2 || !strings.HasPrefix(f[1], "#") {
			continue
		}
		n, err := strconv.Atoi(f[1][1:])
		if err != nil {
			continue
		}
		if cur, ok := out[f[0]]; !ok || n > cur {
			out[f[0]] = n
		}
	}
	return out
}

func handledIdx(handled map[string]int, ruleID string) int {
	if n, ok := handled[ruleID]; ok {
		return n
	}
	return -1
}

// firedSince counts fired rules after t — the per-task budget window.
func firedSince(events []core.HistoryEvent, t time.Time) int {
	n := 0
	for _, ev := range events {
		if ev.Verb == "rule" && ev.At.After(t) {
			n++
		}
	}
	return n
}
