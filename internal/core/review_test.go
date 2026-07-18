package core

import (
	"strings"
	"testing"
)

const reviewDoc = `# Review · T-0005 · R-001 · коммит a1b2c3d

## [R1] Кнопка видна для пустых отчётов — resolved
src/dashboard.tsx:88 · автор: user

## [R2] Даты в UTC без учёта таймзоны — open
src/reports/csv.ts:42 · автор: reviewer

Отчёт хранит tz в meta, но сериализатор пишет UTC.

- row.push(date.toISOString())
+ row.push(formatInTz(date, report.meta.tz))

## [R3] Нет лимита строк — tech-debt
автор: reviewer
`

func TestParseReviewRound(t *testing.T) {
	r, err := ParseReviewRound(reviewDoc)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 3 {
		t.Fatalf("замечаний %d, ожидалось 3", len(r.Items))
	}
	if r.Items[1].ID != "R2" || r.Items[1].Status != ReviewOpen {
		t.Errorf("R2: %+v", r.Items[1])
	}
	if !strings.Contains(r.Items[1].Body, "formatInTz") {
		t.Errorf("тело R2 потеряло дифф: %q", r.Items[1].Body)
	}
	if !strings.Contains(r.Header, "коммит a1b2c3d") {
		t.Errorf("шапка раунда потеряна: %q", r.Header)
	}

	// Раунд с open — вердикта нет.
	if _, done := r.Verdict(); done {
		t.Error("вердикт не должен вычисляться при open-замечаниях")
	}

	// Триаж: R2 → rework ⇒ вердикт rework.
	if err := r.SetStatus("R2", ReviewRework); err != nil {
		t.Fatal(err)
	}
	if v, done := r.Verdict(); !done || v != "rework" {
		t.Errorf("вердикт = %q, %v; ожидался rework", v, done)
	}

	// R2 → resolved ⇒ все resolved/tech-debt ⇒ done.
	r.SetStatus("R2", ReviewResolved)
	if v, done := r.Verdict(); !done || v != "done" {
		t.Errorf("вердикт = %q, %v; ожидался done", v, done)
	}

	if id := r.NextItemID(); id != "R4" {
		t.Errorf("NextItemID = %q, ожидался R4", id)
	}
}

func TestReviewRoundtrip(t *testing.T) {
	r, err := ParseReviewRound(reviewDoc)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := ParseReviewRound(r.Render())
	if err != nil {
		t.Fatalf("перепарс отрендеренного раунда: %v", err)
	}
	if len(r2.Items) != len(r.Items) {
		t.Errorf("roundtrip потерял замечания: %d != %d", len(r2.Items), len(r.Items))
	}
	for i := range r.Items {
		if r2.Items[i] != r.Items[i] {
			t.Errorf("замечание %d изменилось:\nдо:    %+v\nпосле: %+v", i, r.Items[i], r2.Items[i])
		}
	}
}

func TestParseReviewUnknownStatus(t *testing.T) {
	if _, err := ParseReviewRound("## [R1] Что-то — maybe\n"); err == nil {
		t.Fatal("неизвестный статус должен быть ошибкой")
	}
}
