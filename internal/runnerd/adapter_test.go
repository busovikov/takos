package runnerd

import (
	"errors"
	"reflect"
	"testing"

	"github.com/busovikov/takos/internal/session"
)

// The headless argv contracts are fixtures, same as the interactive ones:
// when an agent CLI changes its flags, these tests scream first.
func TestClaudeArgvFixtures(t *testing.T) {
	a, _ := ByID("claude-code")
	uuid := "8b0734d3-143c-561b-af4a-d35516026c9f"

	got := a.Argv(Request{Bin: "claude", Prompt: "plan it", UUID: uuid})
	want := []string{"claude", "-p", "--session-id", uuid, "plan it"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("new: %q, want %q", got, want)
	}

	got = a.Argv(Request{Bin: "claude", Prompt: "go on", UUID: uuid, Resume: true,
		Model: "claude-haiku-4-5", PermissionMode: "auto"})
	want = []string{"claude", "-p", "--resume", uuid,
		"--model", "claude-haiku-4-5", "--dangerously-skip-permissions", "go on"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resume: %q, want %q", got, want)
	}

	// ollama/* models never leak into claude's --model.
	got = a.Argv(Request{Bin: "claude", Prompt: "x", UUID: uuid, Model: "ollama/gemma4:e4b"})
	want = []string{"claude", "-p", "--session-id", uuid, "x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ollama model leaked: %q", got)
	}
}

func TestOpencodeArgvFixtures(t *testing.T) {
	a, _ := ByID("opencode")
	got := a.Argv(Request{Bin: "opencode", Prompt: "plan it", Model: "ollama/gemma4:e4b"})
	want := []string{"opencode", "run", "--model", "ollama/gemma4:e4b", "plan it"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("new: %q, want %q", got, want)
	}
	got = a.Argv(Request{Bin: "opencode", Prompt: "go on", Resume: true})
	want = []string{"opencode", "run", "--continue", "go on"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resume: %q, want %q", got, want)
	}
}

func TestClassification(t *testing.T) {
	a, _ := ByID("claude-code")
	fail := errors.New("exit status 1")
	cases := []struct {
		err        error
		transcript string
		want       string
	}{
		{nil, "all good", session.OutcomeFinished},
		{nil, "", session.OutcomeFinished},
		{fail, "You have hit your usage limit, try later", session.OutcomeLimit},
		{fail, "5-hour Rate Limit reached", session.OutcomeLimit},
		{fail, "panic: something broke", session.OutcomeInterrupted},
		{fail, "", session.OutcomeInterrupted},
	}
	for _, tc := range cases {
		if got := a.Classify(tc.err, tc.transcript); got != tc.want {
			t.Errorf("Classify(%v, %q) = %s, want %s", tc.err, tc.transcript, got, tc.want)
		}
	}
}

func TestUnknownAdapter(t *testing.T) {
	if _, ok := ByID("gemini"); ok {
		t.Error("gemini has no headless adapter yet — ByID must refuse")
	}
}
