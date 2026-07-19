package rules

import (
	"fmt"
	"strings"
)

// The rule condition language is deliberately tiny: clauses joined by &&,
// each one "<var> == <value>" or "<var> != <value>". Values are bare words
// (optionally quoted); unknown variables evaluate to "". Enough for
// `role == handover && outcome == finished` — anything richer belongs in
// dedicated rule events, not in an expression engine.

// Eval evaluates a rule's `if` condition against the event's variables.
// An empty expression is true.
func Eval(expr string, vars map[string]string) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true, nil
	}
	for _, clause := range strings.Split(expr, "&&") {
		neg := false
		parts := strings.SplitN(clause, "==", 2)
		if len(parts) != 2 {
			parts = strings.SplitN(clause, "!=", 2)
			neg = true
		}
		if len(parts) != 2 {
			return false, fmt.Errorf("condition %q: want <var> == <value> or <var> != <value>", strings.TrimSpace(clause))
		}
		name := strings.TrimSpace(parts[0])
		want := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if name == "" || want == "" {
			return false, fmt.Errorf("condition %q: empty side", strings.TrimSpace(clause))
		}
		if (vars[name] == want) == neg {
			return false, nil
		}
	}
	return true, nil
}
