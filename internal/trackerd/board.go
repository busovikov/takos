package trackerd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/session"
)

// The web API projects a workspace into JSON the way the TUI projects it
// into a terminal: the same read-first views (board, card, review round)
// off the same filesystem. Nothing here is authoritative — every field is
// re-derived from the task folders on each request, so a projection can
// never drift from the files.

// SessionProj is a task's latest session as the UI shows it.
type SessionProj struct {
	Name      string    `json:"name"`
	Running   bool      `json:"running"`
	Outcome   string    `json:"outcome,omitempty"`
	StartedAt time.Time `json:"startedAt,omitzero"`
	EndedAt   time.Time `json:"endedAt,omitzero"`
}

// BoardTask is one card on the staircase.
type BoardTask struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Priority    string       `json:"priority"`
	Stage       string       `json:"stage"`
	Area        string       `json:"area"`             // sprint | backlog
	Sprint      string       `json:"sprint,omitempty"` // set when area is "sprint"
	Repo        string       `json:"repo,omitempty"`
	Labels      []string     `json:"labels,omitempty"`
	Paused      bool         `json:"paused,omitempty"`
	OpenReviews int          `json:"openReviews,omitempty"`
	Session     *SessionProj `json:"session,omitempty"`
}

// Board is the /api/ws/{name}/board payload: the active sprint's stages and
// the tasks positioned in them, plus the backlog.
type Board struct {
	Workspace string      `json:"workspace"`
	Sprint    string      `json:"sprint,omitempty"`
	Stages    []string    `json:"stages"`
	Tasks     []BoardTask `json:"tasks"`
	Backlog   []BoardTask `json:"backlog"`
}

// buildBoard projects the workspace's active sprint into a Board.
func buildBoard(name string, ws *core.Workspace) (*Board, error) {
	b := &Board{Workspace: name}
	for _, s := range ws.WF.Stages {
		b.Stages = append(b.Stages, s.ID)
	}
	active, err := ws.ActiveSprint()
	if err == nil {
		b.Sprint = active.Name
	}
	handlers, err := ws.ListHandlers("")
	if err != nil {
		return nil, err
	}
	for _, h := range handlers {
		base := filepath.Base(h.Path)
		if strings.HasPrefix(base, ".") {
			continue
		}
		id := core.HandlerID(h.Path)
		taskDir, err := ws.FindTaskDir(id)
		if err != nil {
			continue
		}
		task, err := core.LoadTask(taskDir)
		if err != nil {
			continue
		}
		bt := BoardTask{
			ID: id, Title: task.Meta.Title, Priority: task.Meta.Priority,
			Stage: h.Stage, Area: h.Area, Sprint: h.Sprint, Repo: task.Meta.Repo,
			Labels: task.Meta.Labels, Paused: task.Meta.Automation == "paused",
			Session: latestSession(taskDir),
		}
		if n, err := core.OpenReviewCount(taskDir); err == nil {
			bt.OpenReviews = n
		}
		switch {
		case h.Area == core.AreaBacklog:
			b.Backlog = append(b.Backlog, bt)
		case active != nil && h.Sprint == active.Name:
			b.Tasks = append(b.Tasks, bt)
		case h.Area == core.AreaSprint:
			// A task in a non-active sprint (e.g. "next") isn't on the active
			// staircase — surface it in the backlog list so it stays visible;
			// its Sprint field marks it as not eligible for stage moves.
			b.Backlog = append(b.Backlog, bt)
		}
	}
	sortTasks(b.Tasks)
	sortTasks(b.Backlog)
	return b, nil
}

// latestSession projects a task's most recent session marker.
func latestSession(taskDir string) *SessionProj {
	ms := session.Markers(taskDir)
	if len(ms) == 0 {
		return nil
	}
	m := ms[len(ms)-1]
	return &SessionProj{
		Name: m.Name, Running: m.Running(), Outcome: m.Outcome,
		StartedAt: m.StartedAt, EndedAt: m.EndedAt,
	}
}

// sortTasks orders cards by id — stable and predictable for the UI.
func sortTasks(ts []BoardTask) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
}

// ReviewItemProj is one finding.
type ReviewItemProj struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Body   string `json:"body"`
}

// ReviewRoundProj is the latest review round with its computed verdict.
type ReviewRoundProj struct {
	File    string           `json:"file"`
	Items   []ReviewItemProj `json:"items"`
	Verdict string           `json:"verdict,omitempty"` // "" until every finding is triaged
	Done    bool             `json:"done"`
}

// HistoryLineProj is one journal entry.
type HistoryLineProj struct {
	At    time.Time `json:"at"`
	Actor string    `json:"actor"`
	Verb  string    `json:"verb"`
	Args  string    `json:"args"`
}

// TaskCard is the /api/ws/{name}/task/{id} payload: everything the TUI card
// shows — the frontmatter, the markdown sections, the artifacts, sessions,
// the latest review round and the journal tail.
type TaskCard struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Priority   string            `json:"priority"`
	Repo       string            `json:"repo,omitempty"`
	Labels     []string          `json:"labels,omitempty"`
	Paused     bool              `json:"paused,omitempty"`
	Stage      string            `json:"stage"`
	Area       string            `json:"area"`
	Sprint     string            `json:"sprint,omitempty"`
	Objective  string            `json:"objective,omitempty"`
	Context    string            `json:"context,omitempty"`
	Plan       string            `json:"plan,omitempty"`
	HandOff    string            `json:"handOff,omitempty"`
	WhatIsDone string            `json:"whatIsDone,omitempty"`
	Docs       string            `json:"docs,omitempty"`
	Testing    string            `json:"testing,omitempty"`
	Sessions   []SessionProj     `json:"sessions,omitempty"`
	Review     *ReviewRoundProj  `json:"review,omitempty"`
	History    []HistoryLineProj `json:"history,omitempty"`
}

// buildCard projects one task into a TaskCard.
func buildCard(ws *core.Workspace, id string) (*TaskCard, error) {
	taskDir, err := ws.FindTaskDir(id)
	if err != nil {
		return nil, err
	}
	task, err := core.LoadTask(taskDir)
	if err != nil {
		return nil, err
	}
	pos, err := ws.FindHandler(id)
	if err != nil {
		return nil, err
	}
	c := &TaskCard{
		ID: id, Title: task.Meta.Title, Priority: task.Meta.Priority,
		Repo: task.Meta.Repo, Labels: task.Meta.Labels,
		Paused: task.Meta.Automation == "paused",
		Stage:  pos.Stage, Area: pos.Area, Sprint: pos.Sprint,
		Objective:  core.GetSection(task.Body, "Objective"),
		Context:    core.GetSection(task.Body, "Context"),
		WhatIsDone: core.GetSection(task.Body, "What is done"),
		Docs:       core.GetSection(task.Body, "Docs"),
		Testing:    core.GetSection(task.Body, "Testing"),
	}
	c.Plan = readArtifact(taskDir, core.PlanFile)
	// The implementation digest is primary; before implementation the
	// planning digest is what exists.
	if h := readArtifact(taskDir, core.HandOffFile); h != "" {
		c.HandOff = h
	} else {
		c.HandOff = readArtifact(taskDir, core.PlanHandOffFile)
	}
	for _, m := range session.Markers(taskDir) {
		c.Sessions = append(c.Sessions, SessionProj{
			Name: m.Name, Running: m.Running(), Outcome: m.Outcome,
			StartedAt: m.StartedAt, EndedAt: m.EndedAt,
		})
	}
	c.Review = latestReview(taskDir)
	c.History = historyTail(taskDir, 40)
	return c, nil
}

func readArtifact(taskDir, name string) string {
	data, err := os.ReadFile(filepath.Join(taskDir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// latestReview projects the newest review round.
func latestReview(taskDir string) *ReviewRoundProj {
	round, path, err := core.LoadLatestRound(taskDir)
	if err != nil || round == nil {
		return nil
	}
	rp := &ReviewRoundProj{File: filepath.Base(path)}
	for _, it := range round.Items {
		rp.Items = append(rp.Items, ReviewItemProj{
			ID: it.ID, Title: it.Title, Status: string(it.Status), Body: it.Body,
		})
	}
	rp.Verdict, rp.Done = round.Verdict()
	return rp
}

// historyTail returns the last n journal entries, newest last.
func historyTail(taskDir string, n int) []HistoryLineProj {
	events, err := core.ReadHistory(taskDir)
	if err != nil {
		return nil
	}
	if len(events) > n {
		events = events[len(events)-n:]
	}
	out := make([]HistoryLineProj, 0, len(events))
	for _, e := range events {
		out = append(out, HistoryLineProj{At: e.At, Actor: e.Actor, Verb: e.Verb, Args: e.Args})
	}
	return out
}
