package cli

import "testing"

func TestVersionField(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2.1.214 (Claude Code)", "2.1.214"},
		{"mock 1.0", "1.0"},
		{"", ""},
		{"no digits here", "no digits here"},
	}
	for _, tc := range cases {
		if got := versionField(tc.in); got != tc.want {
			t.Errorf("versionField(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestContains(t *testing.T) {
	if !contains([]string{"a", "b"}, "b") {
		t.Error("want true")
	}
	if contains([]string{"a", "b"}, "c") {
		t.Error("want false")
	}
	if contains(nil, "a") {
		t.Error("want false on nil")
	}
}
