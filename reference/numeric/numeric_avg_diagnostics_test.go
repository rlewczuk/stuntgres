package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericAvgDiagnostics(t *testing.T) {
	numericAvgScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE t(i INTEGER,n NUMERIC,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO t VALUES (1,1,1)", 1))
		missing := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguous := "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, c := range []struct {
			query, code, message, hint, position string
			args                                 []any
		}{
			{"SELECT avg(NULL) FROM t", "42725", "function avg(unknown) is not unique", ambiguous, "8", nil},
			{"SELECT avg('1') FROM t", "42725", "function avg(unknown) is not unique", ambiguous, "8", nil},
			{"SELECT avg($1) FROM t", "42725", "function avg(unknown) is not unique", ambiguous, "8", []any{1}},
			{"SELECT avg(1,2) FROM t", "42883", "function avg(integer, integer) does not exist", missing, "8", nil},
			{"SELECT avg(avg(i)) FROM t", "42803", "aggregate function calls cannot be nested", "", "12", nil},
			{"SELECT i FROM t WHERE avg(i)=1", "42803", "aggregate functions are not allowed in WHERE", "", "23", nil},
			{"SELECT i FROM t GROUP BY avg(i)", "42803", "aggregate functions are not allowed in GROUP BY", "", "26", nil},
			{"INSERT INTO t(i) VALUES (1) RETURNING avg(i)", "42803", "aggregate functions are not allowed in RETURNING", "", "39", nil},
			{"SELECT avg('oops'::numeric) FROM t", "22P02", "invalid input syntax for type numeric: \"oops\"", "", "12", nil},
			{"SELECT avg('oops'::double precision) FROM t", "22P02", "invalid input syntax for type double precision: \"oops\"", "", "12", nil},
		} {
			t.Run(c.query, func(t *testing.T) {
				diagnostic := fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint, Position: c.position}
				fixture.IntervalAvgDiagnostic(t, p, conn, recorder, c.query, diagnostic, c.args...)
			})
		}
		for _, value := range []string{"oops", ""} {
			for _, kind := range []string{"numeric", "real", "double precision"} {
				for _, prepared := range []bool{false, true} {
					context := "unnamed portal parameter $1 = '...'"
					if value == "" {
						context = "unnamed portal parameter $1 = ''"
					}
					s := fixture.ImplicitError("SELECT avg($1::"+kind+") FROM t", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type " + kind + ": \"" + value + "\"", Context: context}, value)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
			}
		}
		q := "SELECT avg(" + strings.TrimSuffix(strings.Repeat("i,", 100), ",") + ") FROM t"
		fixture.IntervalAvgDiagnostic(t, p, conn, recorder, q, fixture.Diagnostic{Code: "42883", Message: "function avg(" + strings.TrimSuffix(strings.Repeat("integer, ", 100), ", ") + ") does not exist", Hint: missing, Position: "8"})
		q = "SELECT avg(" + strings.TrimSuffix(strings.Repeat("i,", 101), ",") + ") FROM t"
		fixture.IntervalAvgDiagnostic(t, p, conn, recorder, q, fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
		run(fixture.Exec("DELETE FROM t", 1))
		fixture.IntervalAvgDiagnostic(t, p, conn, recorder, "SELECT avg(TRUE) FROM t", fixture.Diagnostic{Code: "42883", Message: "function avg(boolean) does not exist", Hint: missing, Position: "8"})
	})
}
