package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/agents"
)

// newDoctorCmd builds `takos doctor`: an advisory health check, not a gate.
// Nothing here is a hard dependency of the binary — takos itself needs no
// external tools — so doctor always exits 0 except on a genuinely broken
// workspace (invalid workflow.yaml), which is a real, actionable problem.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Health check: agent CLIs, ollama, workflow.yaml role assignments",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runDoctor()
			return nil
		},
	}
}

func runDoctor() {
	fmt.Println("agents:")
	detected := map[string]agents.Info{}
	for _, a := range agents.Detect() {
		detected[a.ID] = a
		if !a.Available {
			fmt.Printf("  ⚠ %-12s not found — install: %s\n", a.ID, a.Install)
			continue
		}
		fmt.Printf("  ✔ %-12s %s\n", a.ID, orDash(a.Version))
	}
	if cc, ok := detected["claude-code"]; ok && cc.Available && cc.Version != "" {
		if agents.CompareVersions(versionField(cc.Version), agents.PinnedClaudeVersion) != 0 {
			fmt.Printf("  ⚠ claude-code: detected %s, argv contract last verified against %s — re-check --session-id/--resume behavior\n",
				cc.Version, agents.PinnedClaudeVersion)
		}
	}

	fmt.Println("\nollama (optional — local models via `ollama launch`):")
	if v, ok := agents.OllamaVersion(); !ok {
		fmt.Println("  · not installed — see https://ollama.com or `brew install ollama`")
	} else if agents.CompareVersions(v, agents.MinOllamaVersion) < 0 {
		fmt.Printf("  ⚠ version %s is older than %s — `ollama launch` claude integration needs %s+\n",
			v, agents.MinOllamaVersion, agents.MinOllamaVersion)
	} else {
		fmt.Printf("  ✔ version %s\n", v)
	}

	ws, err := openWSRaw()
	if err != nil {
		fmt.Printf("\nworkspace: ⚠ %v\n", err)
		return
	}
	fmt.Printf("\nworkspace: %s\n", ws.Root)
	if errs := ws.WF.Validate(); len(errs) > 0 {
		fmt.Println("  ✗ workflow.yaml is invalid:")
		for _, e := range errs {
			fmt.Printf("    - %s\n", e)
		}
		return
	}
	fmt.Println("  ✔ workflow.yaml is valid")

	for _, name := range sortedRoleNames(ws.WF.Roles) {
		r := ws.WF.Roles[name]
		a, known := detected[r.Agent]
		switch {
		case !known:
			fmt.Printf("  ⚠ role %-12s agent %q is not a supported id (takos agents)\n", name, r.Agent)
		case !a.Available:
			fmt.Printf("  ⚠ role %-12s agent %q not found — install: %s\n", name, r.Agent, a.Install)
		case strings.HasPrefix(r.Model, "ollama/") && !contains(a.Models, r.Model):
			fmt.Printf("  ⚠ role %-12s model %q is not pulled — run: ollama pull %s\n",
				name, r.Model, strings.TrimPrefix(r.Model, "ollama/"))
		default:
			fmt.Printf("  ✔ role %-12s %s %s\n", name, r.Agent, orDash(r.Model))
		}
	}
}

// versionField takes the leading token of a version string ("claude-code
// 2.0.0 (Claude Code)" → "2.0.0") — agent CLIs vary in how much they print
// after --version.
func versionField(v string) string {
	f := strings.Fields(v)
	for _, tok := range f {
		if len(tok) > 0 && (tok[0] >= '0' && tok[0] <= '9') {
			return tok
		}
	}
	return v
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
