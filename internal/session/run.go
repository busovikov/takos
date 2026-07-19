package session

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/busovikov/takos/internal/agents"
	"github.com/busovikov/takos/internal/core"
)

// Exec is a ready-to-launch session command. The TUI runs it through
// tea.Exec (releasing the terminal), the CLI blocks on Run — both go
// through the same Begin/End bookkeeping.
type Exec struct {
	TaskID  string
	TaskDir string
	Stage   string
	Role    string
	Name    string
	UUID    string
	Resume  bool
	Argv    []string
	Dir     string // child cwd: the task's repo, else the task folder
}

// Plan loads everything needed to enter a session for a task: its current
// stage and the entry-policy verdict. Refuses when a session is already
// live, or the task has no stage to work in (backlog/archive).
func Plan(ws *core.Workspace, id string) (*core.Task, *core.HandlerPos, EntryPlan, error) {
	var none EntryPlan
	taskDir, err := ws.FindTaskDir(id)
	if err != nil {
		return nil, nil, none, err
	}
	task, err := core.LoadTask(taskDir)
	if err != nil {
		return nil, nil, none, err
	}
	pos, err := ws.FindHandler(id)
	if err != nil {
		return nil, nil, none, err
	}
	if pos.Area == core.AreaArchive {
		return nil, nil, none, fmt.Errorf("%s is archived (%s) — no sessions", id, pos.Sprint)
	}
	if pos.Stage == core.Backlog {
		return nil, nil, none, fmt.Errorf("%s is in the backlog — pull it into a sprint first (takos task take %s)", id, id)
	}

	RepairStale(taskDir)
	if m := Active(taskDir); m != nil {
		return nil, nil, none, fmt.Errorf("session %s is already running (pid %d) — takos task cancel %s",
			m.Name, m.PID, id)
	}
	events, err := core.ReadHistory(taskDir)
	if err != nil {
		return nil, nil, none, err
	}
	return task, pos, PlanEntry(events, id, pos.Stage), nil
}

// Build assembles the Exec for a resolved entry choice: resume=true
// continues plan.Resume*, otherwise a fresh session plan.New* is opened
// with the role's prompt. roleOverride replaces the stage's conventional
// role (used by `takos task agent --role`).
func Build(ws *core.Workspace, task *core.Task, pos *core.HandlerPos, plan EntryPlan, resume bool, roleOverride string) (*Exec, error) {
	role := roleOverride
	if role == "" {
		role = RoleForStage(pos.Stage)
	}
	bin, err := agentBin(ws, role)
	if err != nil {
		return nil, err
	}

	e := &Exec{
		TaskID:  task.Meta.ID,
		TaskDir: task.Dir,
		Stage:   pos.Stage,
		Role:    role,
		Resume:  resume,
		Dir:     task.Meta.Repo,
	}
	if e.Dir == "" {
		e.Dir = task.Dir
	}
	if resume {
		if plan.ResumeName == "" {
			return nil, fmt.Errorf("%s: nothing to resume", task.Meta.ID)
		}
		e.Name, e.UUID = plan.ResumeName, plan.ResumeUUID
		e.Argv = ResumeArgv(bin, e.UUID)
	} else {
		e.Name, e.UUID = plan.NewName, plan.NewUUID
		e.Argv = NewArgv(bin, e.UUID, Prompt(ws, task, task.Dir, role))
	}
	return e, nil
}

// agentBin resolves the role's agent to an installed binary; a missing
// agent is a refusal with the install hint.
func agentBin(ws *core.Workspace, role string) (string, error) {
	agentID := "claude-code"
	if role != "" {
		if r, ok := ws.WF.Roles[role]; ok && r.Agent != "" {
			agentID = r.Agent
		}
	}
	info, ok := agents.ByID(agentID)
	if !ok {
		return "", fmt.Errorf("unknown agent %q", agentID)
	}
	if !info.Available {
		return "", fmt.Errorf("agent %s is not installed (%s)", agentID, info.Install)
	}
	return info.Path, nil
}

// Command builds the child process: stdio inherited (a plain child on the
// user's terminal, no PTY), cwd in the task's repo.
func (e *Exec) Command() *exec.Cmd {
	cmd := exec.Command(e.Argv[0], e.Argv[1:]...)
	cmd.Dir = e.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}

// Begin records a session start: a marker with the live pid and a history
// line. actor is "user" for interactive runs, "agent:<role>" for headless
// ones. Called right after the child starts.
func Begin(taskDir, actor, name, uuid string, pid int, resume bool) {
	verb := "start"
	if resume {
		verb = "resume"
	}
	writeMarker(taskDir, &Marker{
		Name: name, UUID: uuid, PID: pid, StartedAt: time.Now(),
	})
	core.AppendHistory(taskDir, actor, "session",
		fmt.Sprintf("%s %s %s", verb, name, uuid))
}

// Finish records a session's terminal outcome in the marker and history.
func Finish(taskDir, actor, name, uuid, outcome string) {
	m := Marker{Name: name, UUID: uuid, StartedAt: time.Now()}
	for _, x := range Markers(taskDir) {
		if x.Name == name {
			m = x
			break
		}
	}
	m.EndedAt = time.Now()
	m.Outcome = outcome
	writeMarker(taskDir, &m)
	core.AppendHistory(taskDir, actor, "session", outcome+" "+name)
}

// Begin records the interactive session start.
func (e *Exec) Begin(pid int) { Begin(e.TaskDir, "user", e.Name, e.UUID, pid, e.Resume) }

// End classifies the exit and records it: exit 0 → finished, anything else
// (signal from `x`/Ctrl-C, non-zero exit) → interrupted. The limit outcome
// is written by the headless runner which can actually detect it.
func (e *Exec) End(waitErr error) string {
	outcome := OutcomeFinished
	if waitErr != nil {
		outcome = OutcomeInterrupted
	}
	Finish(e.TaskDir, "user", e.Name, e.UUID, outcome)
	return outcome
}

// Run blocks until the session ends (the CLI path); the TUI drives the same
// Begin/End around tea.Exec instead.
func (e *Exec) Run() (string, error) {
	cmd := e.Command()
	if err := cmd.Start(); err != nil {
		return "", err
	}
	e.Begin(cmd.Process.Pid)
	return e.End(cmd.Wait()), nil
}

// Cancel sends SIGTERM to the task's live session; the launching process
// observes the exit and records the interrupted outcome.
func Cancel(taskDir string) (*Marker, error) {
	m := Active(taskDir)
	if m == nil {
		return nil, fmt.Errorf("no running session")
	}
	if err := syscall.Kill(m.PID, syscall.SIGTERM); err != nil {
		return nil, err
	}
	return m, nil
}
