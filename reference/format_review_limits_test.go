package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceFormatReviewLimits(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE format_review_limits(id integer)", 0))
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			for _, nested := range []bool{false, true} {
				name := `"FORMAT"`
				if nested {
					name = "absent_review_call"
				}
				for _, n := range []int{100, 101} {
					arguments := strings.TrimSuffix(strings.Repeat("NULL,", n), ",")
					call := name + "(" + arguments + ")"
					if nested {
						call = "format(NULL," + call + ")"
					}
					q := prefix + "SELECT " + call + " FROM format_review_limits"
					position := referencePosition(len([]rune(q[:strings.Index(q, name)])) + 1)
					d := controlsReferenceDiagnostic("54023", "cannot pass more than 100 arguments to a function")
					if n == 100 {
						diagnosticName := "FORMAT"
						if nested {
							diagnosticName = name
						}
						d = controlsReferenceDiagnostic("42883", "function "+diagnosticName+"("+strings.TrimSuffix(strings.Repeat("unknown, ", n), ", ")+") does not exist")
						d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					}
					d.Position = position
					t.Run(name+"-"+referencePosition(n)+prefix, func(t *testing.T) { formatReferenceFailure(t, p, b, c, q, d, true) })
				}
				for _, invalidAt := range []int{0, 100} {
					arguments := make([]string, 101)
					for i := range arguments {
						arguments[i] = "NULL"
					}
					arguments[invalidAt] = "absent"
					call := name + "(" + strings.Join(arguments, ",") + ")"
					if nested {
						call = "format(NULL," + call + ")"
					}
					q := prefix + "SELECT " + call + " FROM format_review_limits"
					d := controlsReferenceDiagnostic("42703", `column "absent" does not exist`)
					d.Position = referencePosition(len([]rune(q[:strings.LastIndex(q, "absent")])) + 1)
					t.Run(name+"-invalid-"+referencePosition(invalidAt)+prefix, func(t *testing.T) { formatReferenceFailure(t, p, b, c, q, d, true) })
				}
			}
			// Global function count precedes namespace resolution after child analysis.
			for _, call := range []string{
				"format_review_missing.format(" + strings.TrimSuffix(strings.Repeat("NULL,", 101), ",") + ")",
				"format(NULL,format_review_missing.absent_review_call(" + strings.TrimSuffix(strings.Repeat("NULL,", 101), ",") + "))",
			} {
				q := prefix + "SELECT " + call + " FROM format_review_limits"
				d := controlsReferenceDiagnostic("54023", "cannot pass more than 100 arguments to a function")
				d.Position = referencePosition(len([]rune(q[:strings.Index(q, "format_review_missing")])) + 1)
				t.Run(q, func(t *testing.T) { formatReferenceFailure(t, p, b, c, q, d, true) })
			}
		}
	})
}
