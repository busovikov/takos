package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
	"github.com/busovikov/takos/internal/trackerd"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Workspace registry: the daemon watches every registered workspace",
	}
	cmd.AddCommand(newWorkspaceAddCmd(), newWorkspaceRemoveCmd(),
		newWorkspaceListCmd())
	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "add [DIR]",
		Short: "Register a workspace (default: current directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			// Only actual workspaces are registrable.
			ws, err := core.Open(core.WorkspaceDir(abs))
			if err != nil {
				return fmt.Errorf("%s is not a takos workspace (run takos task init there)", dir)
			}
			reg, err := registry.Load()
			if err != nil {
				return err
			}
			entry, err := reg.Add(name, ws.Root)
			if err != nil {
				return err
			}
			if err := reg.Save(); err != nil {
				return err
			}
			ok("workspace %q registered: %s", entry.Name, entry.Root)
			if trackerCtl.running() {
				ok("the running daemon picks it up automatically")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "registry name (default: directory basename)")
	return cmd
}

func newWorkspaceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a workspace (files are not touched)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := registry.Load()
			if err != nil {
				return err
			}
			if err := reg.Remove(args[0]); err != nil {
				return err
			}
			if err := reg.Save(); err != nil {
				return err
			}
			ok("workspace %q unregistered", args[0])
			return nil
		},
	}
}

func newWorkspaceListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Registered workspaces (+ daemon state when it is running)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := registry.Load()
			if err != nil {
				return err
			}
			// Daemon state per workspace, when the daemon answers.
			states := map[string]trackerd.WorkspaceSummary{}
			if trackerCtl.running() {
				if _, addr, err := trackerCtl.readPid(); err == nil {
					var wss []trackerd.WorkspaceSummary
					if fetchJSON(addr, "/api/workspaces", &wss) == nil {
						for _, ws := range wss {
							states[ws.Name] = ws
						}
					}
				}
			}
			entries := reg.Entries()
			if jsonOut {
				type row struct {
					registry.Entry
					State string `json:"state,omitempty"`
				}
				out := make([]row, 0, len(entries))
				for _, e := range entries {
					out = append(out, row{Entry: e, State: states[e.Name].State})
				}
				return json.NewEncoder(os.Stdout).Encode(out)
			}
			if len(reg.Workspaces) == 0 {
				fmt.Println("no workspaces yet — run takos task init in a project directory")
			}
			// Entries come most recently used first — the head of the list is
			// what the picker offers by default.
			for i, e := range entries {
				mark := " "
				if i == 0 {
					mark = "*"
				}
				state := ""
				if s, okk := states[e.Name]; okk {
					state = " · " + s.State
					if s.State == trackerd.StateRunning && s.ActiveSprint != "" {
						state += " · " + s.ActiveSprint
					}
				}
				used := ""
				if !e.LastUsedAt.IsZero() {
					used = " · " + ago(e.LastUsedAt)
				}
				fmt.Printf(" %s %-14s %s%s%s\n", mark, e.Name, e.Root, state, used)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return cmd
}
