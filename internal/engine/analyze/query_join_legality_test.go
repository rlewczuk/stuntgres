package analyze

import "testing"

func TestQueryOuterJoinsComparisonNegators(t *testing.T) {
	for _, tc := range []struct{ operator, negator string }{
		{"=", "<>"}, {"<>", "="}, {"<", ">="}, {"<=", ">"}, {">", "<="}, {">=", "<"},
	} {
		t.Run(tc.operator, func(t *testing.T) {
			if got := fullJoinComparisonOperator(tc.operator, false); got != tc.operator {
				t.Fatalf("nonnegated operator=%q", got)
			}
			if got := fullJoinComparisonOperator(tc.operator, true); got != tc.negator {
				t.Fatalf("negator=%q; want %q", got, tc.negator)
			}
		})
	}
	if got := fullJoinComparisonOperator("unsupported", true); got != "" {
		t.Fatalf("unsupported comparison acquired negator %q", got)
	}
}
