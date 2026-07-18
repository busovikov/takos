package core

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

// A review round is reviews/R-00N.md. Every finding is a block headed by
//
//	## [R2] <title> — <status>
//
// The status lives right in the block heading; the round's verdict is never
// written down — it is computed from the statuses (no brittle "Verdict: …").

type ReviewStatus string

const (
	ReviewOpen     ReviewStatus = "open"
	ReviewResolved ReviewStatus = "resolved"
	ReviewRework   ReviewStatus = "rework"
	ReviewTechDebt ReviewStatus = "tech-debt"
)

var reviewStatuses = map[ReviewStatus]bool{
	ReviewOpen: true, ReviewResolved: true, ReviewRework: true, ReviewTechDebt: true,
}

type ReviewItem struct {
	ID     string // R1, R2, …
	Title  string
	Status ReviewStatus
	Body   string // everything until the next block: description, file:line, diff
}

type ReviewRound struct {
	Header string // lines before the first block (round title, commit)
	Items  []ReviewItem
}

var reviewItemRe = regexp.MustCompile(`^## \[(R\d+)\] (.+?) — (\S+)\s*$`)

// ParseReviewRound parses a reviews/R-00N.md document.
func ParseReviewRound(doc string) (*ReviewRound, error) {
	round := &ReviewRound{}
	var cur *ReviewItem
	var buf strings.Builder
	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimRight(buf.String(), "\n")
			round.Items = append(round.Items, *cur)
		} else {
			round.Header = strings.TrimRight(buf.String(), "\n")
		}
		buf.Reset()
	}

	sc := bufio.NewScanner(strings.NewReader(doc))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := reviewItemRe.FindStringSubmatch(line); m != nil {
			flush()
			st := ReviewStatus(m[3])
			if !reviewStatuses[st] {
				return nil, fmt.Errorf("finding %s: unknown status %q", m[1], m[3])
			}
			cur = &ReviewItem{ID: m[1], Title: m[2], Status: st}
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return round, nil
}

// Render serialises the round back to markdown.
func (r *ReviewRound) Render() string {
	var b strings.Builder
	if r.Header != "" {
		b.WriteString(r.Header)
		b.WriteString("\n")
	}
	for _, it := range r.Items {
		fmt.Fprintf(&b, "## [%s] %s — %s\n", it.ID, it.Title, it.Status)
		if it.Body != "" {
			b.WriteString(it.Body)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// SetStatus updates one item's status; unknown id or status is an error.
func (r *ReviewRound) SetStatus(id string, st ReviewStatus) error {
	if !reviewStatuses[st] {
		return fmt.Errorf("unknown status %q", st)
	}
	for i := range r.Items {
		if r.Items[i].ID == id {
			r.Items[i].Status = st
			return nil
		}
	}
	return fmt.Errorf("finding %s not found", id)
}

// NextItemID returns the next free item id (R<n+1>).
func (r *ReviewRound) NextItemID() string {
	max := 0
	for _, it := range r.Items {
		var n int
		fmt.Sscanf(it.ID, "R%d", &n)
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("R%d", max+1)
}

// Counts returns per-status totals.
func (r *ReviewRound) Counts() map[ReviewStatus]int {
	c := map[ReviewStatus]int{}
	for _, it := range r.Items {
		c[it.Status]++
	}
	return c
}

// Verdict of a fully triaged round: any open → not done ("", false);
// any rework → "rework"; otherwise → "done".
func (r *ReviewRound) Verdict() (string, bool) {
	c := r.Counts()
	if c[ReviewOpen] > 0 {
		return "", false
	}
	if c[ReviewRework] > 0 {
		return "rework", true
	}
	return "done", true
}
