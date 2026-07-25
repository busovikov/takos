package core

import (
	"os"
	"path/filepath"
	"time"

	"github.com/busovikov/takos/internal/fsstore"
)

// SprintMeta is the frontmatter of sprint.md inside a sprint folder.
// The invariant "sprints/ holds exactly one active and one next sprint"
// is maintained by createTask/sprint close and repaired by the reconciler.
type SprintMeta struct {
	Status     string    `yaml:"status"` // active | next
	StartedAt  time.Time `yaml:"startedAt,omitempty"`
	EndsAt     time.Time `yaml:"endsAt,omitempty"`
	LengthDays int       `yaml:"lengthDays"`
}

const (
	SprintFile        = "sprint.md"
	SprintActive      = "active"
	SprintNext        = "next"
	DefaultSprintDays = 14
)

// LoadSprint reads sprintDir/sprint.md.
func LoadSprint(sprintDir string) (*SprintMeta, error) {
	doc, err := os.ReadFile(filepath.Join(sprintDir, SprintFile))
	if err != nil {
		return nil, err
	}
	var meta SprintMeta
	if _, err := fsstore.ParseFrontmatter(doc, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// SaveSprint atomically rewrites sprintDir/sprint.md.
func SaveSprint(sprintDir string, meta *SprintMeta) error {
	doc, err := fsstore.RenderFrontmatter(meta, nil)
	if err != nil {
		return err
	}
	return fsstore.WriteFileAtomic(filepath.Join(sprintDir, SprintFile), doc, 0o644)
}
