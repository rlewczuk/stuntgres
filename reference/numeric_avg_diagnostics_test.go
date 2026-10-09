package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceNumericAvgDiagnostics(t *testing.T) {
	numericAvgScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE t(i INTEGER,n NUMERIC,d DOUBLE PRECISION)", 0))
		run(referenceExec("INSERT INTO t VALUES (1,1,1)", 1))
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
				diagnostic := referenceDiagnostic{Code: c.code, Message: c.message, Hint: c.hint, Position: c.position}
				// INSERT RETURNING remains an independent native syntax exclusion;
				// PostgreSQL still resolves AVG and rejects its RETURNING placement.
				if !pg && strings.HasPrefix(c.query, "INSERT ") {
					diagnostic = referenceDiagnostic{Code: "0A000", Message: "INSERT suffix is unsupported", Position: referencePosition(strings.Index(c.query, "RETURNING") + 1)}
				}
				intervalAvgDiagnostic(t, p, conn, recorder, c.query, diagnostic, c.args...)
			})
		}
		for _, value := range []string{"oops", ""} {
			for _, kind := range []string{"numeric", "real", "double precision"} {
				for _, prepared := range []bool{false, true} {
					context := "unnamed portal parameter $1 = '...'"
					if value == "" {
						context = "unnamed portal parameter $1 = ''"
					}
					s := referenceImplicitError("SELECT avg($1::"+kind+") FROM t", referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type " + kind + ": \"" + value + "\"", Context: context}, value)
					s.selectRows = true
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
			}
		}
		q := "SELECT avg(" + strings.TrimSuffix(strings.Repeat("i,", 100), ",") + ") FROM t"
		intervalAvgDiagnostic(t, p, conn, recorder, q, referenceDiagnostic{Code: "42883", Message: "function avg(" + strings.TrimSuffix(strings.Repeat("integer, ", 100), ", ") + ") does not exist", Hint: missing, Position: "8"})
		q = "SELECT avg(" + strings.TrimSuffix(strings.Repeat("i,", 101), ",") + ") FROM t"
		intervalAvgDiagnostic(t, p, conn, recorder, q, referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
		run(referenceExec("DELETE FROM t", 1))
		intervalAvgDiagnostic(t, p, conn, recorder, "SELECT avg(TRUE) FROM t", referenceDiagnostic{Code: "42883", Message: "function avg(boolean) does not exist", Hint: missing, Position: "8"})
	})
}
