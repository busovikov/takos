package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reviewDoc = `# Review · T-0005 · R-001 · commit a1b2c3d

## [R1] Export button visible for empty reports — resolved
src/dashboard.tsx:88 · author: user

## [R2] Dates serialized in UTC ignoring the report timezone — open
src/reports/csv.ts:42 · author: reviewer

The report keeps tz in meta but the serializer always writes UTC.

- row.push(date.toISOString())
+ row.push(formatInTz(date, report.meta.tz))

## [R3] No row limit on export — tech-debt
author: reviewer
`

func TestParseReviewRound(t *testing.T) {
	r, err := ParseReviewRound(reviewDoc)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(r.Items))
	}
	if r.Items[1].ID != "R2" || r.Items[1].Status != ReviewOpen {
		t.Errorf("R2: %+v", r.Items[1])
	}
	if !strings.Contains(r.Items[1].Body, "formatInTz") {
		t.Errorf("R2 body lost the diff: %q", r.Items[1].Body)
	}
	if !strings.Contains(r.Header, "commit a1b2c3d") {
		t.Errorf("round header lost: %q", r.Header)
	}

	// A round with open items has no verdict.
	if _, done := r.Verdict(); done {
		t.Error("verdict must not be computed while items are open")
	}

	// Triage: R2 → rework ⇒ verdict rework.
	if err := r.SetStatus("R2", ReviewRework); err != nil {
		t.Fatal(err)
	}
	if v, done := r.Verdict(); !done || v != "rework" {
		t.Errorf("verdict = %q, %v; want rework", v, done)
	}

	// R2 → resolved ⇒ all resolved/tech-debt ⇒ done.
	r.SetStatus("R2", ReviewResolved)
	if v, done := r.Verdict(); !done || v != "done" {
		t.Errorf("verdict = %q, %v; want done", v, done)
	}

	if id := r.NextItemID(); id != "R4" {
		t.Errorf("NextItemID = %q, want R4", id)
	}
}

func TestReviewRoundtrip(t *testing.T) {
	r, err := ParseReviewRound(reviewDoc)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := ParseReviewRound(r.Render())
	if err != nil {
		t.Fatalf("re-parsing the rendered round: %v", err)
	}
	if len(r2.Items) != len(r.Items) {
		t.Errorf("roundtrip lost items: %d != %d", len(r2.Items), len(r.Items))
	}
	for i := range r.Items {
		if r2.Items[i] != r.Items[i] {
			t.Errorf("item %d changed:\nbefore: %+v\nafter:  %+v", i, r.Items[i], r2.Items[i])
		}
	}
}

func TestParseReviewUnknownStatus(t *testing.T) {
	if _, err := ParseReviewRound("## [R1] Something — maybe\n"); err == nil {
		t.Fatal("an unknown status must be an error")
	}
}

func TestAppendReworkToHandoff(t *testing.T) {
	dir := t.TempDir()
	round := &ReviewRound{Items: []ReviewItem{
		{ID: "R1", Title: "Fix the race", Status: ReviewRework},
		{ID: "R2", Title: "Style nit", Status: ReviewResolved},
		{ID: "R3", Title: "Missing test", Status: ReviewRework},
	}}
	// hand-off.md already has the executor's sections — only "From review"
	// changes.
	seed := "## Done\nsteps 1-3\n\n## From review\n- [R-001.md · R9] older item\n"
	if err := os.WriteFile(filepath.Join(dir, HandoffFileName), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AppendReworkToHandoff(dir, "T-0001", "reviews/R-002.md", round); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, HandoffFileName))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	sec := GetSection(body, "From review")
	for _, want := range []string{"older item", "[R-002.md · R1] Fix the race", "[R-002.md · R3] Missing test"} {
		if !strings.Contains(sec, want) {
			t.Errorf("From review misses %q:\n%s", want, sec)
		}
	}
	if strings.Contains(sec, "Style nit") {
		t.Errorf("resolved finding leaked into hand-off:\n%s", sec)
	}
	if GetSection(body, "Done") != "steps 1-3" {
		t.Errorf("Done section was disturbed:\n%s", body)
	}

	// A round with no rework findings must not create or touch the file.
	empty := t.TempDir()
	clean := &ReviewRound{Items: []ReviewItem{{ID: "R1", Title: "ok", Status: ReviewResolved}}}
	if err := AppendReworkToHandoff(empty, "T-0002", "reviews/R-001.md", clean); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(empty, HandoffFileName)); !os.IsNotExist(err) {
		t.Error("hand-off.md appeared without rework findings")
	}
}
