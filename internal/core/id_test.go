package core

import "testing"

func TestValidTaskID(t *testing.T) {
	valid := []string{"T-0001", "T-0042", "T-12345"}
	for _, id := range valid {
		if !ValidTaskID(id) {
			t.Errorf("ValidTaskID(%q) = false, want true", id)
		}
	}
	invalid := []string{"", "T-1", "T-042", "t-0001", "T-0001x", "0001", "T_0001"}
	for _, id := range invalid {
		if ValidTaskID(id) {
			t.Errorf("ValidTaskID(%q) = true, want false", id)
		}
	}
}
