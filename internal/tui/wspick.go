package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
	"github.com/busovikov/takos/internal/watch"
)

// The W screen: switch the board to another registered workspace without
// leaving the TUI. The board is just a projection of a root — switching is
// "change root, re-arm the watcher, reload".

type wsPickState struct {
	entries []registry.Entry
	sel     int
}

func (a *App) openWsPick() tea.Cmd {
	reg, err := registry.Load()
	if err != nil {
		return a.flash(stErr, "✗ %v", err)
	}
	if len(reg.Workspaces) == 0 {
		return a.flash(stDim, "no registered workspaces (takos workspace add)")
	}
	st := &wsPickState{entries: reg.Entries()}
	for i, e := range st.entries {
		if e.Root == a.ws.Root {
			st.sel = i
		}
	}
	a.wp = st
	a.scr = scrWsPick
	return nil
}

func (a *App) updateWsPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return a, nil
	}
	wp := a.wp
	switch key.String() {
	case "esc", "q", "W":
		a.scr = scrBoard
	case "up", "k":
		if wp.sel > 0 {
			wp.sel--
		}
	case "down", "j":
		if wp.sel < len(wp.entries)-1 {
			wp.sel++
		}
	case "enter":
		return a, a.switchWorkspace(wp.entries[wp.sel])
	}
	return a, nil
}

func (a *App) switchWorkspace(e registry.Entry) tea.Cmd {
	if e.Root == a.ws.Root {
		a.scr = scrBoard
		return nil
	}
	ws, err := core.Open(e.Root)
	if err != nil {
		return a.flash(stErr, "✗ %s: %v", e.Name, err)
	}
	a.ws = ws
	registry.Touch(ws.Root) // this workspace now heads the picker
	// Re-arm the live board on the new root.
	if a.fsw != nil {
		a.fsw.Close()
		a.fsw = nil
	}
	if w, err := watch.New(ws.Root, 300*time.Millisecond); err == nil {
		a.fsw = w
	}
	a.cur = 0
	a.reload()
	a.scr = scrBoard
	return tea.Batch(a.waitFs(), a.flash(stOK, "✔ workspace: %s (%s)", e.Name, e.Root))
}

// Pick runs the workspace picker on its own, before any board exists: the
// answer to starting takos outside every workspace. Entries come most
// recently used first, so Enter on the pre-selected head resumes the last
// workspace worked on. An empty name means the user backed out.
func Pick(entries []registry.Entry) (registry.Entry, error) {
	p := &pickApp{entries: entries}
	if _, err := tea.NewProgram(p, tea.WithAltScreen()).Run(); err != nil {
		return registry.Entry{}, err
	}
	return p.chosen, nil
}

type pickApp struct {
	entries []registry.Entry
	sel     int
	chosen  registry.Entry
}

func (p *pickApp) Init() tea.Cmd { return nil }

func (p *pickApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return p, nil
	}
	switch key.String() {
	case "esc", "q", "ctrl+c":
		return p, tea.Quit
	case "up", "k":
		if p.sel > 0 {
			p.sel--
		}
	case "down", "j":
		if p.sel < len(p.entries)-1 {
			p.sel++
		}
	case "enter":
		p.chosen = p.entries[p.sel]
		return p, tea.Quit
	}
	return p, nil
}

func (p *pickApp) View() string {
	var b strings.Builder
	b.WriteString(" " + stTitle.Render("WORKSPACES") + stDim.Render("   "+registry.Path()) + "\n")
	b.WriteString(" " + stDim.Render("you are outside every workspace — pick one") + "\n\n")
	for i, e := range p.entries {
		line := fmt.Sprintf("%-14s %s", e.Name, e.Root)
		if i == p.sel {
			b.WriteString(" " + stAccent.Render("❯") + " " + stSel.Render(line) + "\n")
		} else {
			b.WriteString("   " + line + "\n")
		}
	}
	b.WriteString("\n " + stDim.Render("↑↓ · Enter open · Esc quit") + "\n")
	return b.String()
}

func (a *App) viewWsPick() string {
	wp := a.wp
	var b strings.Builder
	b.WriteString(" " + stTitle.Render("WORKSPACES") + stDim.Render("   "+registry.Path()) + "\n\n")
	for i, e := range wp.entries {
		mark := "  "
		if e.Root == a.ws.Root {
			mark = stOK.Render("● ")
		}
		line := fmt.Sprintf("%s%-14s %s", mark, e.Name, e.Root)
		if i == wp.sel {
			b.WriteString(" " + stAccent.Render("❯") + " " + stSel.Render(line) + "\n")
		} else {
			b.WriteString("   " + line + "\n")
		}
	}
	b.WriteString("\n " + stDim.Render("↑↓ · Enter switch · Esc back") + "\n")
	return b.String()
}
