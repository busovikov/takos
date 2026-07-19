// Package registry is the machine-local list of workspaces:
// ~/.takos/workspaces.yaml. Workspaces themselves stay portable git repos
// and know nothing about the registry — it only holds pointers. The daemon
// supervises every registered workspace; the CLI resolves --workspace names
// through it.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/fsstore"
)

const FileName = "workspaces.yaml"

// Entry points at one workspace. Name is the stable identifier used in
// `--workspace`, the TUI picker and /api/ws/{name}/… URLs.
type Entry struct {
	Name string `yaml:"name"`
	Root string `yaml:"root"`
}

// Registry is the parsed workspaces.yaml.
type Registry struct {
	Default    string  `yaml:"default,omitempty"`
	Workspaces []Entry `yaml:"workspaces"`
}

// Home is the machine-local takos directory: $TAKOS_HOME or ~/.takos.
// The registry, the daemon's global lock/pid/log all live here.
func Home() string {
	if v := os.Getenv("TAKOS_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".takos"
	}
	return filepath.Join(home, ".takos")
}

// Path is the registry file location.
func Path() string { return filepath.Join(Home(), FileName) }

// Load reads the registry; a missing file is an empty registry, not an
// error. Roots are normalized: a leading "~/" (hand-edited entries) expands
// to the home directory.
func Load() (*Registry, error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return &Registry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(), err)
	}
	for i := range r.Workspaces {
		r.Workspaces[i].Root = expandHome(r.Workspaces[i].Root)
	}
	return &r, nil
}

// Save atomically rewrites the registry file.
func (r *Registry) Save() error {
	if err := os.MkdirAll(Home(), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return fsstore.WriteFileAtomic(Path(), data, 0o644)
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Find returns the entry with the given name.
func (r *Registry) Find(name string) (Entry, bool) {
	for _, e := range r.Workspaces {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Add registers a workspace. An empty name defaults to the slugified
// directory basename; duplicates by name or resolved path are rejected. The
// first registered workspace becomes the default.
func (r *Registry) Add(name, root string) (Entry, error) {
	abs, err := filepath.Abs(expandHome(root))
	if err != nil {
		return Entry{}, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if name == "" {
		name = core.Slugify(filepath.Base(abs))
		if !nameRe.MatchString(name) {
			name = "ws-" + name // basenames like "2024" need a letter first
		}
	}
	if !nameRe.MatchString(name) {
		return Entry{}, fmt.Errorf("workspace name %q must match [a-z][a-z0-9-]*", name)
	}
	if _, ok := r.Find(name); ok {
		return Entry{}, fmt.Errorf("workspace %q is already registered", name)
	}
	for _, e := range r.Workspaces {
		other := e.Root
		if resolved, err := filepath.EvalSymlinks(other); err == nil {
			other = resolved
		}
		if other == abs {
			return Entry{}, fmt.Errorf("%s is already registered as %q", abs, e.Name)
		}
	}
	entry := Entry{Name: name, Root: abs}
	r.Workspaces = append(r.Workspaces, entry)
	if r.Default == "" {
		r.Default = name
	}
	return entry, nil
}

// Remove drops a workspace from the registry (the workspace itself is not
// touched). A default pointing at it moves to the first remaining entry.
func (r *Registry) Remove(name string) error {
	for i, e := range r.Workspaces {
		if e.Name != name {
			continue
		}
		r.Workspaces = append(r.Workspaces[:i], r.Workspaces[i+1:]...)
		if r.Default == name {
			r.Default = ""
			if len(r.Workspaces) > 0 {
				r.Default = r.Workspaces[0].Name
			}
		}
		return nil
	}
	return fmt.Errorf("workspace %q is not registered", name)
}

// Use sets the default workspace.
func (r *Registry) Use(name string) error {
	if _, ok := r.Find(name); !ok {
		return fmt.Errorf("workspace %q is not registered", name)
	}
	r.Default = name
	return nil
}

// Entries returns registered workspaces; an empty registry yields the single
// implicit default workspace — the pre-registry behavior.
func (r *Registry) Entries() []Entry {
	if len(r.Workspaces) == 0 {
		return []Entry{{Name: "default", Root: core.DefaultRoot()}}
	}
	return r.Workspaces
}

// NameFor maps a workspace root to its registry name (the implicit single
// default workspace counts); ok=false means the root is not registered, so
// name-addressed daemon APIs cannot reach it.
func NameFor(root string) (string, bool) {
	reg, err := Load()
	if err != nil {
		return "", false
	}
	want := canonPath(root)
	for _, e := range reg.Entries() {
		if canonPath(e.Root) == want {
			return e.Name, true
		}
	}
	return "", false
}

// canonPath normalizes a path for identity comparison: home expansion,
// absolutization, symlink resolution.
func canonPath(p string) string {
	abs, err := filepath.Abs(expandHome(p))
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// Resolve maps a --workspace value (registry name or path) to a workspace
// root. Precedence with an empty flag: $TAKOS_WORKSPACE → registry default
// → ~/.takos/workspace.
func Resolve(flag string) (string, error) {
	if flag != "" {
		if looksLikePath(flag) {
			return filepath.Abs(expandHome(flag))
		}
		reg, err := Load()
		if err != nil {
			return "", err
		}
		if e, ok := reg.Find(flag); ok {
			return e.Root, nil
		}
		if _, err := os.Stat(flag); err == nil {
			return filepath.Abs(flag)
		}
		return "", fmt.Errorf("no workspace named %q in the registry (takos workspace list)", flag)
	}
	if v := os.Getenv("TAKOS_WORKSPACE"); v != "" {
		return v, nil
	}
	reg, err := Load()
	if err != nil {
		return "", err
	}
	if reg.Default != "" {
		if e, ok := reg.Find(reg.Default); ok {
			return e.Root, nil
		}
	}
	return core.DefaultRoot(), nil
}

// looksLikePath distinguishes a filesystem path from a registry name:
// anything with a separator or a ./~ prefix is a path.
func looksLikePath(s string) bool {
	return strings.ContainsRune(s, os.PathSeparator) ||
		strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~")
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}
