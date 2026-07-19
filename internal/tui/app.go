// Package tui is the interactive terminal UI (Bubble Tea): the staircase
// board, task card with an action menu, review carousel, backlog, sprint and
// role screens. Every action goes through the same core validators as the
// CLI — the TUI is just a skin over the filesystem.
package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/session"
	"github.com/busovikov/takos/internal/watch"
)

type screen int

const (
	scrBoard screen = iota
	scrCard
	scrCarousel
	scrBacklog
	scrSprint
	scrRoles
	scrWsPick
)

var (
	stAccent   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	stDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	stOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	stErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	stSel      = lipgloss.NewStyle().Bold(true)
	stHeadSel  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	stDiffAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	stDiffDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	stKey      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	stTitle    = lipgloss.NewStyle().Bold(true)
	stStatusOK = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

// App is the single Bubble Tea model; scr selects the active screen.
type App struct {
	ws   *core.Workspace
	repo string // repo filter ("" — no filter)
	w, h int

	scr    screen
	status string
	stID   int

	rows     []row // board rows (active sprint)
	cur      int
	backlogN int

	prompt  *promptState
	confirm *confirmState
	card    *cardState
	car     *carState
	bl      *backlogState
	sp      *sprintState
	rl      *rolesState
	wp      *wsPickState

	fsw *watch.Watcher // live board: nil when fsnotify is unavailable
}

type row struct {
	ID, Title, Prio, Stage, Sprint, Area, Dir string
	StageIdx                                  int
	Running                                   bool // a live agent session (▶)
}

type clearStatusMsg struct{ id int }
type editorDoneMsg struct{}
type fsChangedMsg struct{}

// Run starts the TUI over an opened workspace.
func Run(ws *core.Workspace, repo string) error {
	a := &App{ws: ws, repo: repo}
	a.reload()
	// Live board: external mv and daemon rules redraw the staircase. The
	// TUI stays functional without the watcher (reloads on own actions).
	if w, err := watch.New(ws.Root, 300*time.Millisecond); err == nil {
		a.fsw = w
		defer w.Close()
	}
	_, err := tea.NewProgram(a, tea.WithAltScreen()).Run()
	return err
}

func (a *App) Init() tea.Cmd { return a.waitFs() }

// waitFs arms a one-shot wait on the watcher channel.
func (a *App) waitFs() tea.Cmd {
	if a.fsw == nil {
		return nil
	}
	return func() tea.Msg {
		if _, ok := <-a.fsw.C; !ok {
			return nil
		}
		return fsChangedMsg{}
	}
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
		return a, nil
	case clearStatusMsg:
		if m.id == a.stID {
			a.status = ""
		}
		return a, nil
	case editorDoneMsg:
		a.reload()
		if a.scr == scrCard && a.card != nil {
			a.openCard(a.card.id)
		}
		return a, nil
	case sessionDoneMsg:
		// The agent session ended — rescan and return to the card, per the
		// contract "exit → the card with a rescan".
		a.reload()
		a.openCard(m.id)
		if a.card != nil && a.card.id == m.id {
			if line := sessionLine(a.card.task.Dir); line != "" {
				return a, a.flash(stDim, "%s", line)
			}
		}
		return a, nil
	case fsChangedMsg:
		// Live board: an external mv or a daemon rule changed the
		// workspace — re-read the filesystem and redraw. Modal input and
		// the triage carousel are left alone mid-flow.
		a.reload()
		if a.prompt == nil && a.confirm == nil {
			switch a.scr {
			case scrCard:
				if a.card != nil {
					a.openCard(a.card.id)
				}
			case scrBacklog:
				a.refreshBacklog()
			}
		}
		return a, a.waitFs()
	}

	// Modal layers first: confirm, then prompt.
	if a.confirm != nil {
		return a.updateConfirm(msg)
	}
	if a.prompt != nil {
		return a.updatePrompt(msg)
	}

	switch a.scr {
	case scrBoard:
		return a.updateBoard(msg)
	case scrCard:
		return a.updateCard(msg)
	case scrCarousel:
		return a.updateCarousel(msg)
	case scrBacklog:
		return a.updateBacklog(msg)
	case scrSprint:
		return a.updateSprint(msg)
	case scrRoles:
		return a.updateRoles(msg)
	case scrWsPick:
		return a.updateWsPick(msg)
	}
	return a, nil
}

func (a *App) View() string {
	var body string
	switch a.scr {
	case scrBoard:
		body = a.viewBoard()
	case scrCard:
		body = a.viewCard()
	case scrCarousel:
		body = a.viewCarousel()
	case scrBacklog:
		body = a.viewBacklog()
	case scrSprint:
		body = a.viewSprint()
	case scrRoles:
		body = a.viewRoles()
	case scrWsPick:
		body = a.viewWsPick()
	}
	return body
}

// flash shows a transient status-bar message for a few seconds.
func (a *App) flash(style lipgloss.Style, format string, args ...any) tea.Cmd {
	a.status = style.Render(fmt.Sprintf(format, args...))
	a.stID++
	id := a.stID
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{id} })
}

// reload rebuilds the board rows from the filesystem.
func (a *App) reload() {
	a.rows = nil
	a.backlogN = 0
	active, err := a.ws.ActiveSprint()
	if err != nil {
		return
	}
	handlers, err := a.ws.ListHandlers("")
	if err != nil {
		return
	}
	for _, h := range handlers {
		r, ok := a.rowFor(h)
		if !ok {
			continue
		}
		if h.Area == core.AreaBacklog {
			a.backlogN++
			continue
		}
		if h.Sprint != active.Name {
			continue
		}
		a.rows = append(a.rows, r)
	}
	sortBoardRows(a.rows)
	if a.cur >= len(a.rows) {
		a.cur = max(0, len(a.rows)-1)
	}
}

// rowFor loads task metadata behind a handler and applies the repo filter.
func (a *App) rowFor(h core.HandlerPos) (row, bool) {
	id := handlerBaseID(h.Path)
	r := row{ID: id, Stage: h.Stage, Sprint: h.Sprint, Area: h.Area}
	if i, ok := a.ws.WF.StageIndex(h.Stage); ok {
		r.StageIdx = i
	}
	dir, err := a.ws.FindTaskDir(id)
	if err != nil {
		return r, false
	}
	r.Dir = dir
	r.Running = session.Active(dir) != nil
	task, err := core.LoadTask(dir)
	if err != nil {
		return r, true
	}
	r.Title, r.Prio = task.Meta.Title, task.Meta.Priority
	if a.repo != "" && task.Meta.Repo != "" && task.Meta.Repo != a.repo {
		return r, false
	}
	return r, true
}

func sortBoardRows(rows []row) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			x, y := rows[j-1], rows[j]
			if x.StageIdx > y.StageIdx || (x.StageIdx == y.StageIdx && x.Prio > y.Prio) {
				rows[j-1], rows[j] = y, x
			} else {
				break
			}
		}
	}
}

func handlerBaseID(path string) string {
	base := path
	if i := lastSlash(base); i >= 0 {
		base = base[i+1:]
	}
	// "T-0005-slug" → "T-0005"
	dash := 0
	for i, c := range base {
		if c == '-' {
			dash++
			if dash == 2 {
				return base[:i]
			}
		}
	}
	return base
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
