// Package agents detects supported coding-agent CLIs installed on the system
// and the models they can run. Role assignments in workflow.yaml are validated
// against this detection.
package agents

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Known is the registry of supported agents, in display order.
var Known = []Spec{
	{ID: "claude-code", Bin: "claude", Install: "npm i -g @anthropic-ai/claude-code",
		Models: []string{"claude-sonnet-5", "claude-opus-4-8", "claude-haiku-4-5"}},
	{ID: "opencode", Bin: "opencode", Install: "npm i -g opencode-ai"},
	{ID: "codex", Bin: "codex", Install: "npm i -g @openai/codex"},
	{ID: "gemini", Bin: "gemini", Install: "npm i -g @google/gemini-cli"},
}

type Spec struct {
	ID      string
	Bin     string
	Install string
	Models  []string // static list; dynamic entries (ollama) are added on detection
}

type Info struct {
	Spec
	Available bool
	Version   string
	Path      string
}

// Detect probes every known agent (LookPath + `--version` with a timeout).
func Detect() []Info {
	out := make([]Info, 0, len(Known))
	for _, spec := range Known {
		info := Info{Spec: spec}
		if path, err := exec.LookPath(spec.Bin); err == nil {
			info.Available = true
			info.Path = path
			info.Version = probeVersion(path)
			if spec.ID == "opencode" {
				info.Models = append(info.Models, ollamaModels()...)
			}
		}
		out = append(out, info)
	}
	return out
}

// ByID returns detection info for one agent id.
func ByID(id string) (Info, bool) {
	for _, info := range Detect() {
		if info.ID == id {
			return info, true
		}
	}
	return Info{}, false
}

func probeVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(out))
	if i := strings.IndexByte(v, '\n'); i > 0 {
		v = v[:i]
	}
	if len(v) > 40 {
		v = v[:40]
	}
	return v
}

// ollamaModels lists local models as "ollama/<name>" when ollama is installed.
func ollamaModels() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ollama", "list").Output()
	if err != nil {
		return nil
	}
	var models []string
	for i, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i == 0 { // table header
			continue
		}
		if f := strings.Fields(line); len(f) > 0 {
			models = append(models, "ollama/"+f[0])
		}
	}
	return models
}
