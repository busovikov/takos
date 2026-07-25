package rules

import "testing"

func TestEval(t *testing.T) {
	vars := map[string]string{"role": "handover", "outcome": "finished"}
	cases := []struct {
		expr    string
		want    bool
		wantErr bool
	}{
		{"", true, false},
		{"role == handover", true, false},
		{"role == reviewer", false, false},
		{"role != reviewer", true, false},
		{"role == handover && outcome == finished", true, false},
		{"role == handover && outcome == limit", false, false},
		{`role == "handover"`, true, false},
		{"missing == handover", false, false},
		{"missing != handover", true, false},
		{"role = handover", false, true},
		{"role ==", false, true},
	}
	for _, tc := range cases {
		got, err := Eval(tc.expr, vars)
		if (err != nil) != tc.wantErr {
			t.Errorf("Eval(%q) error = %v, wantErr %v", tc.expr, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("Eval(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}
