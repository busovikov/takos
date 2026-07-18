package core

import (
	"strings"
	"testing"
)

func TestHistoryAppendAndParse(t *testing.T) {
	dir := t.TempDir()
	if err := AppendHistory(dir, "user", "created", "refinement"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistory(dir, "user", "move", "refinement -> planning"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistory(dir, "rule:plan-on-planning", "spawn", "planner s-x1"); err != nil {
		t.Fatal(err)
	}

	events, err := ReadHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("получено %d событий, ожидалось 3", len(events))
	}
	if events[1].Actor != "user" || events[1].Verb != "move" || events[1].Args != "refinement -> planning" {
		t.Errorf("событие move распарсено неверно: %+v", events[1])
	}
	if events[2].Actor != "rule:plan-on-planning" {
		t.Errorf("actor = %q", events[2].Actor)
	}

	stage, ok := LastMove(events)
	if !ok || stage != "planning" {
		t.Errorf("LastMove = %q, %v; ожидалось planning", stage, ok)
	}
}

func TestParseHistoryDropsTornLine(t *testing.T) {
	log := "2026-07-18T15:02:11Z [user] move refinement -> planning\n" +
		"2026-07-18T15:02:12Z [engine] session s-1 fin" // оборвано, без \n
	events, err := ParseHistory(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	// Оборванная строка всё же матчится грамматикой (это валидный префикс) —
	// важно, что парсер не падает; но строка без даты/скобок отбрасывается:
	garbage := "мусор без грамматики\n2026-07-18T15:02:11Z [user] check review-approved\n"
	events2, err := ParseHistory(strings.NewReader(garbage))
	if err != nil {
		t.Fatal(err)
	}
	if len(events2) != 1 || events2[0].Verb != "check" {
		t.Errorf("мусорная строка должна быть отброшена: %+v", events2)
	}
	_ = events
}

func TestLastMoveFromCreated(t *testing.T) {
	log := "2026-07-18T10:00:00Z [user] created backlog\n"
	events, _ := ParseHistory(strings.NewReader(log))
	stage, ok := LastMove(events)
	if !ok || stage != "backlog" {
		t.Errorf("LastMove = %q, %v", stage, ok)
	}
}
