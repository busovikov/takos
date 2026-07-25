package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/busovikov/takos/internal/fsstore"
)

var roundFileRe = regexp.MustCompile(`^R-(\d{3})\.md$`)

// LatestRoundPath returns the newest reviews/R-00N.md, or "" if none exist.
func LatestRoundPath(taskDir string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(taskDir, ReviewsDir))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	max, name := 0, ""
	for _, e := range entries {
		if m := roundFileRe.FindStringSubmatch(e.Name()); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > max {
				max, name = n, e.Name()
			}
		}
	}
	if name == "" {
		return "", nil
	}
	return filepath.Join(taskDir, ReviewsDir, name), nil
}

// LoadLatestRound parses the newest round; (nil, "", nil) when there is none.
func LoadLatestRound(taskDir string) (*ReviewRound, string, error) {
	path, err := LatestRoundPath(taskDir)
	if err != nil || path == "" {
		return nil, "", err
	}
	doc, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	round, err := ParseReviewRound(string(doc))
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	return round, path, nil
}

// SaveRound atomically rewrites a round file.
func SaveRound(path string, r *ReviewRound) error {
	return fsstore.WriteFileAtomic(path, []byte(r.Render()), 0o644)
}

// NewRoundPath allocates the next round file path (reviews/R-00N.md).
func NewRoundPath(taskDir string) (string, error) {
	dir := filepath.Join(taskDir, ReviewsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	last, err := LatestRoundPath(taskDir)
	if err != nil {
		return "", err
	}
	n := 0
	if last != "" {
		if m := roundFileRe.FindStringSubmatch(filepath.Base(last)); m != nil {
			n, _ = strconv.Atoi(m[1])
		}
	}
	return filepath.Join(dir, fmt.Sprintf("R-%03d.md", n+1)), nil
}

// OpenReviewCount counts open items in the latest round (0 when no rounds).
func OpenReviewCount(taskDir string) (int, error) {
	round, _, err := LoadLatestRound(taskDir)
	if err != nil || round == nil {
		return 0, err
	}
	return round.Counts()[ReviewOpen], nil
}

// AddComment appends a user remark as an open item to the latest round,
// creating R-001 when no round exists yet.
func AddComment(taskDir, id, text string) (itemID string, err error) {
	round, path, err := LoadLatestRound(taskDir)
	if err != nil {
		return "", err
	}
	if round == nil {
		path, err = NewRoundPath(taskDir)
		if err != nil {
			return "", err
		}
		round = &ReviewRound{Header: fmt.Sprintf("# Review · %s · %s",
			id, time.Now().UTC().Format("2006-01-02"))}
	}
	// The block title is the first line of the text, length-bounded.
	title, _, _ := strings.Cut(text, "\n")
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:57]) + "…"
	}
	itemID = round.NextItemID()
	round.Items = append(round.Items, ReviewItem{
		ID: itemID, Title: title, Status: ReviewOpen, Body: "author: user\n\n" + text,
	})
	if err := SaveRound(path, round); err != nil {
		return "", err
	}
	AppendHistory(taskDir, "user", "comment", fmt.Sprintf("%s %s", filepath.Base(path), itemID))
	return itemID, nil
}

// HandoffFileName is the implementation digest artifact in the task folder.
const HandoffFileName = "hand-off.md"

// AppendReworkToHandoff copies a triaged round's rework findings — full
// body, not just the title — into the "From review" section of hand-off.md,
// so the executor's next session opens straight on what actually needs
// fixing, with the command to resolve each one once it's done. No rework
// findings — no touch.
func AppendReworkToHandoff(taskDir, id, roundPath string, r *ReviewRound) error {
	var blocks []string
	for _, it := range r.Items {
		if it.Status != ReviewRework {
			continue
		}
		block := fmt.Sprintf("- [%s · %s] %s", filepath.Base(roundPath), it.ID, it.Title)
		if it.Body != "" {
			block += "\n\n" + it.Body
		}
		block += fmt.Sprintf("\n\n  once fixed: takos task review %s %s --resolve", id, it.ID)
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return nil
	}
	path := filepath.Join(taskDir, HandoffFileName)
	body := ""
	if data, err := os.ReadFile(path); err == nil {
		body = string(data)
	}
	sec := GetSection(body, "From review")
	if sec != "" {
		sec += "\n\n"
	}
	sec += strings.Join(blocks, "\n\n")
	body = SetSection(body, "From review", sec)
	return fsstore.WriteFileAtomic(path, []byte(body), 0o644)
}
