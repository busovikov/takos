package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/core"
)

func newTaskInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the tracker workspace (+ workflow.yaml template)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := workspaceFlag
			if root == "" {
				root = core.DefaultRoot()
			}
			ws, err := core.Init(root)
			if err != nil {
				return err
			}
			active, _ := ws.ActiveSprint()
			ok("workspace: %s", ws.Root)
			ok("workflow.yaml: %d stages, active sprint %s", len(ws.WF.Stages), active.Name)
			return nil
		},
	}
}

func newTaskCreateCmd() *cobra.Command {
	var desc, repo, prio, sprint string
	var backlog bool
	cmd := &cobra.Command{
		Use:   "create \"TITLE\"",
		Short: "Create a task (folder in tasks/ + handler symlink)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			if repo == "" {
				repo = currentRepo()
			}
			task, pos, err := ws.CreateTask(args[0], core.CreateOpts{
				Objective: desc, Repo: repo, Priority: prio,
				Backlog: backlog, Sprint: sprint,
			})
			if err != nil {
				return err
			}
			where := pos.Stage
			if pos.Area == core.AreaSprint {
				where = pos.Sprint + "/" + pos.Stage
			}
			ok("%s created: %s (%s)", task.Meta.ID, filepath.Base(task.Dir), where)
			return nil
		},
	}
	cmd.Flags().StringVarP(&desc, "description", "d", "", "Objective — a short draft of the idea")
	cmd.Flags().StringVar(&repo, "repo", "", "project repository (default: git repo of cwd)")
	cmd.Flags().StringVarP(&prio, "priority", "p", "", "p0…p3 (default p2)")
	cmd.Flags().BoolVar(&backlog, "backlog", false, "create in the backlog")
	cmd.Flags().StringVar(&sprint, "sprint", "", "next — create in the next sprint")
	return cmd
}

func newTaskListCmd() *cobra.Command {
	var jsonOut, backlog, all bool
	var stage string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks (active sprint; --backlog, --all)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			rows, err := collectRows(ws)
			if err != nil {
				return err
			}
			repo := currentRepo()
			var out []taskRow
			backlogCount := 0
			for _, r := range rows {
				if r.Area == core.AreaBacklog {
					backlogCount++
				}
				if !all && repo != "" && r.Repo != "" && r.Repo != repo {
					continue
				}
				if backlog != (r.Area == core.AreaBacklog) {
					continue
				}
				if stage != "" && r.Stage != stage {
					continue
				}
				out = append(out, r)
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(out)
			}
			if len(out) == 0 {
				fmt.Println("no tasks")
				return nil
			}
			printRows(ws, out, backlog, backlogCount)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	cmd.Flags().BoolVar(&backlog, "backlog", false, "show the backlog")
	cmd.Flags().BoolVar(&all, "all", false, "tasks of every repository")
	cmd.Flags().StringVar(&stage, "stage", "", "filter by stage")
	// --plain is accepted for symmetry with the TUI mode (A4); output is
	// always plain for now.
	cmd.Flags().Bool("plain", false, "plain output without the interactive UI")
	return cmd
}

type taskRow struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
	Stage    string `json:"stage"`
	Sprint   string `json:"sprint,omitempty"`
	Area     string `json:"area"`
	Repo     string `json:"repo,omitempty"`
	Updated  string `json:"updated,omitempty"`
}

func collectRows(ws *core.Workspace) ([]taskRow, error) {
	handlers, err := ws.ListHandlers("")
	if err != nil {
		return nil, err
	}
	var rows []taskRow
	for _, h := range handlers {
		id := filepath.Base(h.Path)
		if i := strings.Index(id, "-"); i > 0 {
			if j := strings.Index(id[i+1:], "-"); j > 0 {
				id = id[:i+1+j]
			}
		}
		row := taskRow{ID: id, Stage: h.Stage, Sprint: h.Sprint, Area: h.Area}
		if taskDir, err := ws.FindTaskDir(id); err == nil {
			if task, err := core.LoadTask(taskDir); err == nil {
				row.Title = task.Meta.Title
				row.Priority = task.Meta.Priority
				row.Repo = task.Meta.Repo
			}
			if fi, err := os.Stat(filepath.Join(taskDir, core.TaskFile)); err == nil {
				row.Updated = ago(fi.ModTime())
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func printRows(ws *core.Workspace, rows []taskRow, backlogMode bool, backlogCount int) {
	if !backlogMode {
		if active, err := ws.ActiveSprint(); err == nil {
			fmt.Printf("%s · %d tasks (+%d in backlog)\n\n", active.Name, len(rows), backlogCount)
		}
	} else {
		fmt.Printf("backlog · %d tasks\n\n", len(rows))
	}
	// Sort: stage order, then priority.
	stageIdx := func(s string) int {
		if i, ok := ws.WF.StageIndex(s); ok {
			return i
		}
		return -1
	}
	sortRows(rows, stageIdx)
	fmt.Printf(" %-17s %-7s %-3s %-40s %s\n", "STAGE", "ID", "P", "TASK", "UPDATED")
	for _, r := range rows {
		stage := r.Stage
		if dn, ok := ws.WF.StageDirName(r.Stage); ok {
			stage = dn
		}
		title := r.Title
		if len([]rune(title)) > 40 {
			title = string([]rune(title)[:39]) + "…"
		}
		fmt.Printf(" %-17s %-7s %-3s %-40s %s\n", stage, r.ID, r.Priority, title, r.Updated)
	}
}

func sortRows(rows []taskRow, stageIdx func(string) int) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			if stageIdx(a.Stage) > stageIdx(b.Stage) ||
				(stageIdx(a.Stage) == stageIdx(b.Stage) && a.Priority > b.Priority) {
				rows[j-1], rows[j] = b, a
			} else {
				break
			}
		}
	}
}

func newTaskShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show T-0001",
		Short: "Task card: sections, position, history tail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			taskDir, err := ws.FindTaskDir(args[0])
			if err != nil {
				return err
			}
			task, err := core.LoadTask(taskDir)
			if err != nil {
				return err
			}
			pos, err := ws.FindHandler(args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{
					"meta": task.Meta, "stage": pos.Stage, "sprint": pos.Sprint,
					"area": pos.Area, "dir": taskDir,
				})
			}
			fmt.Printf("%s · %s\n", task.Meta.ID, task.Meta.Title)
			where := pos.Stage
			if pos.Sprint != "" {
				where = pos.Sprint + " / " + pos.Stage
			}
			fmt.Printf("%s · %s · repo: %s · automation: %s\n\n",
				where, task.Meta.Priority, orDash(task.Meta.Repo), task.Meta.Automation)
			for _, sec := range []string{"Objective", "Context", "What is done", "Docs", "Testing"} {
				if text := core.GetSection(task.Body, sec); text != "" {
					fmt.Printf("%s\n%s\n\n", strings.ToUpper(sec), indent(text))
				}
			}
			printFiles(taskDir)
			printHistoryTail(taskDir, 6)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return cmd
}

func printFiles(taskDir string) {
	var parts []string
	if fi, err := os.Stat(filepath.Join(taskDir, core.PlanFile)); err == nil && fi.Size() > 0 {
		state := "not resolved"
		if core.HasCheck(taskDir, "plan-resolved") {
			state = "resolved ✔"
		}
		parts = append(parts, fmt.Sprintf("plan.md (%s)", state))
	}
	if round, path, _ := core.LoadLatestRound(taskDir); round != nil {
		c := round.Counts()
		parts = append(parts, fmt.Sprintf("%s (%d findings, %d open)",
			filepath.Base(path), len(round.Items), c[core.ReviewOpen]))
	}
	if len(parts) > 0 {
		fmt.Printf("FILES  %s\n\n", strings.Join(parts, " · "))
	}
}

func printHistoryTail(taskDir string, n int) {
	events, err := core.ReadHistory(taskDir)
	if err != nil || len(events) == 0 {
		return
	}
	fmt.Println("HISTORY")
	if len(events) > n {
		events = events[len(events)-n:]
	}
	for _, e := range events {
		fmt.Printf("  %s [%s] %s %s\n",
			e.At.Local().Format("02.01 15:04"), e.Actor, e.Verb, e.Args)
	}
}

func newTaskHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history T-0001",
		Short: "Full task history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			taskDir, err := ws.FindTaskDir(args[0])
			if err != nil {
				return err
			}
			events, err := core.ReadHistory(taskDir)
			if err != nil {
				return err
			}
			for _, e := range events {
				fmt.Println(e.String())
			}
			return nil
		},
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}
