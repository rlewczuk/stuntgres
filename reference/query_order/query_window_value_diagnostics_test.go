package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowValueDiagnosticRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, d fixture.Diagnostic, stage string, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s := fixture.ImplicitError(q, d, args...)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared && stage == "prepare" {
				s.ErrorStage = "prepare"
			}
			queryWindowRun(t, p, b, s)
		})
	}
}

func queryWindowValueInvalidFrame(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowValueDiagnosticRoutes(t, p, b, "SELECT first_value(1) OVER(ROWS UNBOUNDED FOLLOWING)", fixture.Diagnostic{Code: "42P20", Message: "frame start cannot be UNBOUNDED FOLLOWING", Position: "33"}, "prepare")
}

func TestReferenceQueryWindowValueDiagnostics(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const signatureHint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for i, tc := range []struct {
			q, code, message string
			pos              int
			hint, stage      string
		}{
			{"SELECT nth_value(1,0) OVER()", "22016", "argument of nth_value must be greater than zero", 0, "", "execute"},
			{"SELECT nth_value(1,-1) OVER()", "22016", "argument of nth_value must be greater than zero", 0, "", "execute"},
			{"SELECT first_value(1)", "42809", "window function first_value requires an OVER clause", 8, "", "prepare"},
			{"SELECT pg_catalog.last_value(1)", "42809", "window function pg_catalog.last_value requires an OVER clause", 8, "", "prepare"},
			{"SELECT first_value(NULL) OVER()", "42804", "could not determine polymorphic type because input has type unknown", 0, "", "prepare"},
			{"SELECT nth_value('x',1) OVER()", "42804", "could not determine polymorphic type because input has type unknown", 0, "", "prepare"},
			{"SELECT nth_value(1,1::bigint) OVER()", "42883", "function nth_value(integer, bigint) does not exist", 8, signatureHint, "prepare"},
			{"SELECT first_value() OVER()", "42883", "function first_value() does not exist", 8, signatureHint, "prepare"},
			{"SELECT nth_value(1,missing) OVER()", "42703", `column "missing" does not exist`, 20, "", "prepare"},
			{"SELECT nth_value(1,'bad') OVER()", "22P02", `invalid input syntax for type integer: "bad"`, 20, "", "prepare"},
			{"SELECT 1 WHERE first_value(1) OVER() > 0", "42P20", "window functions are not allowed in WHERE", 16, "", "prepare"},
			{"SELECT first_value(last_value(1) OVER()) OVER()", "42P20", "window function calls cannot be nested", 20, "", "prepare"},
			{"SELECT first_value(1) OVER missing", "42704", `window "missing" does not exist`, 28, "", "prepare"},
			{"SELECT first_value(1) FILTER(WHERE TRUE) OVER()", "0A000", "FILTER is not implemented for non-aggregate window functions", 8, "", "prepare"},
			{"SELECT first_value(DISTINCT 1) OVER()", "0A000", "DISTINCT is not implemented for window functions", 8, "", "prepare"},
			{"SELECT first_value(1 ORDER BY missing) OVER()", "0A000", "aggregate ORDER BY is not implemented for window functions", 8, "", "prepare"},
			{"SELECT first_value(generate_series(1,2)) OVER()", "0A000", "window function calls cannot contain set-returning function calls", 20, "You might be able to move the set-returning function into a LATERAL FROM item.", "prepare"},
			{"SELECT first_value(1) OVER(ROWS UNBOUNDED FOLLOWING)", "42P20", "frame start cannot be UNBOUNDED FOLLOWING", 33, "", "prepare"},
			{"SELECT first_value(1) OVER(ROWS BETWEEN CURRENT ROW AND 1 PRECEDING)", "42P20", "frame starting from current row cannot have preceding rows", 57, "", "prepare"},
			{"SELECT 1 WHERE nth_value(1,missing) OVER()>0", "42703", `column "missing" does not exist`, 28, "", "prepare"},
			{"SELECT 1 WHERE nth_value(1,1::bigint) OVER()>0", "42883", "function nth_value(integer, bigint) does not exist", 16, signatureHint, "prepare"},
			{"SELECT first_value(last_value(1) OVER()) FILTER(WHERE TRUE) OVER()", "0A000", "FILTER is not implemented for non-aggregate window functions", 8, "", "prepare"},
		} {
			t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(fmt.Sprintf("prefix=%q", prefix), func(t *testing.T) {
						pos := tc.pos
						if pos != 0 {
							pos += len([]rune(prefix))
						}
						queryWindowValueDiagnosticRoutes(t, p, b, prefix+tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(pos), Hint: tc.hint}, tc.stage)
					})
				}
			})
		}
		for _, tc := range []struct {
			n             any
			code, message string
		}{
			{nil, "22004", "frame starting offset must not be null"},
			{-1, "22013", "frame starting offset must not be negative"},
		} {
			t.Run(tc.code, func(t *testing.T) {
				queryWindowValueDiagnosticRoutes(t, p, b, "SELECT first_value(1) OVER(ROWS $1 PRECEDING)", fixture.Diagnostic{Code: tc.code, Message: tc.message}, "execute", tc.n)
				queryWindowValueDiagnosticRoutes(t, p, b, "SELECT first_value(1) OVER(ROWS $1 PRECEDING) WHERE FALSE", fixture.Diagnostic{Code: tc.code, Message: tc.message}, "execute", tc.n)
			})
		}
	})
}
