package core

import (
	"fmt"
	"path/filepath"
)

// TriageResult reports what one triage decision did: the finding's new
// status, any tech-debt task it spawned, and — once the round is fully
// triaged — the computed verdict and the stage the task auto-moved to.
type TriageResult struct {
	ItemID  string
	Status  ReviewStatus
	DebtID  string // tech-debt task created in the backlog, if any
	Verdict string // "" until every finding is triaged
	Done    bool
	MovedTo string // "" while the round still has open findings
}

// TriageFinding applies one triage decision to a task's latest review round
// and, when the round is fully triaged, computes the verdict. A "rework"
// verdict auto-moves the task back to implementation, with the rework
// findings copied into hand-off.md's "From review". A "done" verdict is
// only reported — the task stays in review until a human (or the executor,
// once it has resolved its own rework findings) moves it to done explicitly;
// nothing auto-moves a task there. A tech-debt finding spawns a backlog task
// linking back to its source. The CLI, TUI and web API all triage through
// here, so the behaviour cannot diverge.
func (ws *Workspace) TriageFinding(id, itemID string, status ReviewStatus) (*TriageResult, error) {
	taskDir, err := ws.FindTaskDir(id)
	if err != nil {
		return nil, err
	}
	round, path, err := LoadLatestRound(taskDir)
	if err != nil {
		return nil, err
	}
	if round == nil {
		return nil, fmt.Errorf("%s has no review rounds", id)
	}
	item, ok := round.Item(itemID)
	if !ok {
		return nil, fmt.Errorf("finding %s not found", itemID)
	}
	if err := round.SetStatus(itemID, status); err != nil {
		return nil, err
	}
	if err := SaveRound(path, round); err != nil {
		return nil, err
	}
	AppendHistory(taskDir, "user", "triage",
		fmt.Sprintf("%s %s -> %s", filepath.Base(path), itemID, status))

	res := &TriageResult{ItemID: itemID, Status: status}

	// Tech debt spawns a backlog task pointing at its source.
	if status == ReviewTechDebt {
		if task, err := LoadTask(taskDir); err == nil {
			debt, _, err := ws.CreateTask(item.Title, CreateOpts{
				Objective: fmt.Sprintf("%s\n\nsource: %s · %s#%s",
					item.Body, id, filepath.Base(path), itemID),
				Repo: task.Meta.Repo, Backlog: true,
			})
			if err != nil {
				return res, err
			}
			res.DebtID = debt.Meta.ID
		}
	}

	// A fully triaged round has a verdict. Only "rework" auto-moves — a
	// "done" verdict is left for a human (or the executor) to act on.
	if v, done := round.Verdict(); done {
		res.Verdict, res.Done = v, true
		if v == "rework" {
			// The executor's next session opens on the rework findings.
			AppendReworkToHandoff(taskDir, id, path, round)
			if mr, err := ws.MoveTask(id, "implementation", "engine", ""); err == nil {
				res.MovedTo = mr.To
			}
		}
	}
	return res, nil
}
