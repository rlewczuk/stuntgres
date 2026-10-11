package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceNumericSumDiagnostics(t *testing.T) {
	numericSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE t(i INTEGER,v TEXT,n NUMERIC,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO t VALUES (1,'text',1,1)", 1))
		missing := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguous := "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, c := range []struct {
			q, code, message, hint, token string
			args                          []any
		}{
			{"SELECT sum(NULL) FROM t", "42725", "function sum(unknown) is not unique", ambiguous, "sum", nil},
			{"SELECT sum('1') FROM t", "42725", "function sum(unknown) is not unique", ambiguous, "sum", nil},
			{"SELECT sum($1) FROM t", "42725", "function sum(unknown) is not unique", ambiguous, "sum", []any{1}},
			{"SELECT sum() FROM t", "42883", "function sum() does not exist", missing, "sum", nil},
			{"SELECT sum(*) FROM t", "42883", "function sum() does not exist", missing, "sum", nil},
			{"SELECT sum(1,2) FROM t", "42883", "function sum(integer, integer) does not exist", missing, "sum", nil},
			{"SELECT sum(TRUE) FROM t", "42883", "function sum(boolean) does not exist", missing, "sum", nil},
			{"SELECT sum(v) FROM t", "42883", "function sum(text) does not exist", missing, "sum", nil},
			{"SELECT public.sum(i) FROM t", "42883", "function public.sum(integer) does not exist", missing, "public", nil},
			{"SELECT absent_sum_schema.sum(i) FROM t", "3F000", "schema \"absent_sum_schema\" does not exist", "", "absent", nil},
			{"SELECT sum(sum(i)) FROM t", "42803", "aggregate function calls cannot be nested", "", "sum(i)", nil},
			{"SELECT i FROM t WHERE sum(i)=1", "42803", "aggregate functions are not allowed in WHERE", "", "sum", nil},
			{"SELECT i FROM t GROUP BY sum(i)", "42803", "aggregate functions are not allowed in GROUP BY", "", "sum", nil},
			{"SELECT i,sum(i) FROM t", "42803", "column \"t.i\" must appear in the GROUP BY clause or be used in an aggregate function", "", "i,", nil},
			{"UPDATE t SET i=sum(i)", "42803", "aggregate functions are not allowed in UPDATE", "", "sum", nil},
			{"INSERT INTO t(i) VALUES(sum(1))", "42803", "aggregate functions are not allowed in VALUES", "", "sum", nil},
			{"UPDATE t SET i=2 RETURNING sum(i)", "42803", "aggregate functions are not allowed in RETURNING", "", "sum", nil},
			{"SELECT sum('oops'::numeric) FROM t", "22P02", "invalid input syntax for type numeric: \"oops\"", "", "'oops'", nil},
			{"SELECT sum('oops'::double precision) FROM t", "22P02", "invalid input syntax for type double precision: \"oops\"", "", "'oops'", nil},
			{"SELECT sum(absent,TRUE) FROM t", "42703", "column \"absent\" does not exist", "", "absent", nil},
			{"SELECT public.sum(absent) FROM t", "42703", "column \"absent\" does not exist", "", "absent", nil},
			{"SELECT sum(i) FILTER(WHERE 1) FROM t", "42804", "argument of FILTER must be type boolean, not type integer", "", "1)", nil},
			{"SELECT sum(DISTINCT i ORDER BY i+1) FROM t", "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", "", "i+1", nil},
			{"SELECT sum(i) FILTER(WHERE sum(i)>0) FROM t", "42803", "aggregate functions are not allowed in FILTER", "", "sum(i)>", nil},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + c.q
				t.Run(q, func(t *testing.T) {
					position := utf8.RuneCountInString(q[:strings.Index(q, c.token)]) + 1
					fixture.IntervalAvgDiagnostic(t, p, conn, recorder, q, fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint, Position: fixture.Position(position)}, c.args...)
				})
			}
		}
		for _, n := range []int{100, 101} {
			q := "SELECT sum(" + strings.TrimSuffix(strings.Repeat("i,", n), ",") + ") FROM t"
			d := fixture.Diagnostic{Code: "42883", Message: "function sum(" + strings.TrimSuffix(strings.Repeat("integer, ", n), ", ") + ") does not exist", Hint: missing, Position: "8"}
			if n == 101 {
				d = fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}
			}
			fixture.IntervalAvgDiagnostic(t, p, conn, recorder, q, d)
			if n == 101 {
				q = "SELECT sum(" + strings.TrimSuffix(strings.Repeat("i,", n), ",") + ") FILTER(WHERE 1) FROM t"
				fixture.IntervalAvgDiagnostic(t, p, conn, recorder, q, fixture.Diagnostic{Code: "42804", Message: "argument of FILTER must be type boolean, not type integer", Position: fixture.Position(strings.Index(q, "1)") + 1)})
			}
		}
		for _, kind := range []string{"smallint", "integer", "bigint", "numeric", "real", "double precision"} {
			for _, value := range []string{"oops", ""} {
				context := "unnamed portal parameter $1 = '...'"
				if value == "" {
					context = "unnamed portal parameter $1 = ''"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT sum($1::"+kind+") FROM t", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type " + kind + ": \"" + value + "\"", Context: context}, value)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					run(s)
				}
			}
		}
		run(fixture.Select("SELECT i,v FROM t", []string{"i", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "text"}}))
	})
}
