package tui

import (
	"fmt"
	"io"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/busovikov/takos/internal/session"
)

// Interactive sessions from the card: the TUI releases the terminal
// (tea.Exec), the agent runs as a plain child with inherited stdio, and the
// card reopens with a rescan when it exits.

type sessionDoneMsg struct{ id string }

// sessionProc adapts an *exec.Cmd to tea.ExecCommand while wrapping the run
// with the session bookkeeping (marker + history on start and exit).
type sessionProc struct {
	cmd *exec.Cmd
	e   *session.Exec
}

func (p *sessionProc) Run() error {
	if err := p.cmd.Start(); err != nil {
		return err
	}
	p.e.Begin(p.cmd.Process.Pid)
	p.e.End(p.cmd.Wait())
	return nil // the outcome lives in history/marker, not in the exit path
}

func (p *sessionProc) SetStdin(r io.Reader)  { p.cmd.Stdin = r }
func (p *sessionProc) SetStdout(w io.Writer) { p.cmd.Stdout = w }
func (p *sessionProc) SetStderr(w io.Writer) { p.cmd.Stderr = w }

// runSession launches a built session and returns to the card afterwards.
func (a *App) runSession(e *session.Exec) tea.Cmd {
	id := e.TaskID
	return tea.Exec(&sessionProc{cmd: e.Command(), e: e},
		func(error) tea.Msg { return sessionDoneMsg{id: id} })
}

// buildSession plans and builds an entry in one stroke for the card menu.
func (a *App) buildSession(id string, resume bool, forceNew bool) tea.Cmd {
	task, pos, plan, err := session.Plan(a.ws, id)
	if err != nil {
		return a.flash(stErr, "✗ %v", err)
	}
	if forceNew && plan.Mode == session.ModeResume {
		return a.flash(stErr, "✗ %s hit its limit — resume is the only way in", plan.ResumeName)
	}
	e, err := session.Build(a.ws, task, pos, plan, resume, "")
	if err != nil {
		return a.flash(stErr, "✗ %v", err)
	}
	return a.runSession(e)
}

// sessionLine renders the card's session fact: ▶ running · 12m — or the
// last outcome with its icon.
func sessionLine(taskDir string) string {
	ms := session.Markers(taskDir)
	if len(ms) == 0 {
		return ""
	}
	m := ms[len(ms)-1]
	if m.Running() {
		return fmt.Sprintf("▶ %s · running · %s", m.Name, compactDur(time.Since(m.StartedAt)))
	}
	icon := map[string]string{
		session.OutcomeFinished:    "✔",
		session.OutcomeLimit:       "⚠",
		session.OutcomeInterrupted: "⏸",
	}[m.Outcome]
	if icon == "" {
		icon = "·"
	}
	return fmt.Sprintf("%s %s · %s", icon, m.Name, m.Outcome)
}

func compactDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
