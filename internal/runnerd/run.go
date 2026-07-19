package runnerd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/busovikov/takos/internal/agents"
	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/session"
)

// Job is one headless role run over a task's stage session. The transcript
// streams to tasks/<id>/sessions/<name>.log while the agent works — a crash
// of takos or the agent loses nothing that was already written.
type Job struct {
	TaskID  string
	TaskDir string
	Stage   string
	Role    string
	Actor   string // history actor: agent:<role>
	Name    string
	UUID    string
	Resume  bool
	Argv    []string
	Dir     string // agent cwd: the task's repo, else the task folder
	LogPath string

	adapter Adapter
}

// PlanJob resolves the entry policy for a headless run. With nobody at the
// keyboard the interactive "ask" collapses to resume (continue the
// interrupted session); forceNew starts a fresh generation instead. A limit
// verdict always resumes — forceNew is refused, same as interactively.
func PlanJob(ws *core.Workspace, id, roleOverride string, forceNew bool) (*Job, error) {
	task, pos, plan, err := session.Plan(ws, id)
	if err != nil {
		return nil, err
	}
	if plan.Mode == session.ModeResume && forceNew {
		return nil, fmt.Errorf("%s hit its limit — resuming is the only way in", plan.ResumeName)
	}
	resume := plan.Mode == session.ModeResume || (plan.Mode == session.ModeAsk && !forceNew)

	role := roleOverride
	if role == "" {
		role = session.RoleForStage(pos.Stage)
	}
	roleCfg := ws.WF.Roles[role]
	agentID := roleCfg.Agent
	if agentID == "" {
		agentID = "claude-code"
	}
	adapter, ok := ByID(agentID)
	if !ok {
		return nil, fmt.Errorf("agent %s has no headless adapter (claude-code, opencode)", agentID)
	}
	info, ok := agents.ByID(agentID)
	if !ok || !info.Available {
		return nil, fmt.Errorf("agent %s is not installed (%s)", agentID, info.Install)
	}

	j := &Job{
		TaskID:  task.Meta.ID,
		TaskDir: task.Dir,
		Stage:   pos.Stage,
		Role:    role,
		Actor:   "agent:" + role,
		Resume:  resume,
		Dir:     task.Meta.Repo,
		adapter: adapter,
	}
	if j.Role == "" {
		j.Actor = "agent"
	}
	if j.Dir == "" {
		j.Dir = task.Dir
	}
	if resume {
		j.Name, j.UUID = plan.ResumeName, plan.ResumeUUID
	} else {
		j.Name, j.UUID = plan.NewName, plan.NewUUID
	}
	j.LogPath = filepath.Join(task.Dir, session.SessionsDirName, j.Name+".log")
	j.Argv = adapter.Argv(Request{
		Bin:            info.Path,
		Prompt:         session.Prompt(ws, task, task.Dir, role),
		UUID:           j.UUID,
		Resume:         resume,
		Model:          roleCfg.Model,
		PermissionMode: roleCfg.PermissionMode,
	})
	return j, nil
}

// Execute runs the job to completion: stdout+stderr stream straight into
// the log file (kernel-level, real-time, crash-safe), the marker carries
// the live pid, and the adapter classifies the ending. Cancelling ctx sends
// SIGTERM (escalating to SIGKILL after a grace period).
func (j *Job) Execute(ctx context.Context) (string, error) {
	if err := os.MkdirAll(filepath.Dir(j.LogPath), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(j.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fmt.Fprintf(f, "=== %s · %s · %s\n", time.Now().UTC().Format(time.RFC3339), j.Name, j.Actor)

	cmd := exec.CommandContext(ctx, j.Argv[0], j.Argv[1:]...)
	cmd.Dir = j.Dir
	cmd.Stdout, cmd.Stderr = f, f
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(f, "=== failed to start: %v\n", err)
		return "", err
	}
	session.Begin(j.TaskDir, j.Actor, j.Name, j.UUID, cmd.Process.Pid, j.Resume)

	waitErr := cmd.Wait()
	outcome := j.adapter.Classify(waitErr, tail(j.LogPath, 16*1024))
	fmt.Fprintf(f, "=== %s · %s\n", time.Now().UTC().Format(time.RFC3339), outcome)
	session.Finish(j.TaskDir, j.Actor, j.Name, j.UUID, outcome)
	return outcome, nil
}

// tail reads the last n bytes of a file for outcome classification.
func tail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	if fi.Size() > n {
		f.Seek(fi.Size()-n, 0)
	}
	buf := make([]byte, n)
	read, _ := f.Read(buf)
	return string(buf[:read])
}
