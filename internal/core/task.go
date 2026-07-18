package core

import (
	"os"
	"path/filepath"
	"time"

	"github.com/busovikov/takos/internal/fsstore"
)

// TaskMeta is the frontmatter of task.md. Stage and sprint are deliberately
// absent: they exist only as the handler symlink's position.
type TaskMeta struct {
	ID         string    `yaml:"id"`
	Title      string    `yaml:"title"`
	Repo       string    `yaml:"repo,omitempty"`
	Priority   string    `yaml:"priority"`
	Labels     []string  `yaml:"labels,omitempty"`
	BlockedBy  []string  `yaml:"blockedBy,omitempty"`
	Automation string    `yaml:"automation"` // active | paused
	CreatedAt  time.Time `yaml:"createdAt"`
}

// Task couples parsed metadata with the markdown body of task.md.
type Task struct {
	Meta TaskMeta
	Body string // markdown: ## Objective, ## Context, ## What is done, …
	Dir  string // абсолютный путь к папке задачи в tasks/
}

const TaskFile = "task.md"

// Well-known files inside a task folder.
const (
	PlanFile        = "plan.md"
	PlanHandOffFile = "plan-hand-off.md"
	HandOffFile     = "hand-off.md"
	ReviewsDir      = "reviews"
	SessionsDir     = "sessions"
	ArtifactsDir    = "artifacts"
)

// LoadTask reads and parses taskDir/task.md.
func LoadTask(taskDir string) (*Task, error) {
	doc, err := os.ReadFile(filepath.Join(taskDir, TaskFile))
	if err != nil {
		return nil, err
	}
	var meta TaskMeta
	body, err := fsstore.ParseFrontmatter(doc, &meta)
	if err != nil {
		return nil, err
	}
	return &Task{Meta: meta, Body: string(body), Dir: taskDir}, nil
}

// Save atomically rewrites task.md.
func (t *Task) Save() error {
	doc, err := fsstore.RenderFrontmatter(t.Meta, []byte(t.Body))
	if err != nil {
		return err
	}
	return fsstore.WriteFileAtomic(filepath.Join(t.Dir, TaskFile), doc, 0o644)
}
