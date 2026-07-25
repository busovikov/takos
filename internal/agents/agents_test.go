package agents

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.31.0", "0.31.0", 0},
		{"0.31.2", "0.31.0", 1},
		{"0.30.9", "0.31.0", -1},
		{"1.0", "1.0.0", 0},
		{"2.1.214", "2.0.0", 1},
		{"2.0", "2.0.0", 0},
		{"0.9", "0.10", -1}, // numeric, not lexical, comparison
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
