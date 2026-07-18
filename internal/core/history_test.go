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
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[1].Actor != "user" || events[1].Verb != "move" || events[1].Args != "refinement -> planning" {
		t.Errorf("move event parsed wrong: %+v", events[1])
	}
	if events[2].Actor != "rule:plan-on-planning" {
		t.Errorf("actor = %q", events[2].Actor)
	}

	stage, ok := LastMove(events)
	if !ok || stage != "planning" {
		t.Errorf("LastMove = %q, %v; want planning", stage, ok)
	}
}

func TestParseHistoryDropsGarbage(t *testing.T) {
	log := "garbage that matches no grammar\n" +
		"2026-07-18T15:02:11Z [user] check review-approved\n"
	events, err := ParseHistory(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Verb != "check" {
		t.Errorf("garbage line must be dropped: %+v", events)
	}
}

func TestLastMoveFromCreated(t *testing.T) {
	log := "2026-07-18T10:00:00Z [user] created backlog\n"
	events, _ := ParseHistory(strings.NewReader(log))
	stage, ok := LastMove(events)
	if !ok || stage != "backlog" {
		t.Errorf("LastMove = %q, %v", stage, ok)
	}
}
