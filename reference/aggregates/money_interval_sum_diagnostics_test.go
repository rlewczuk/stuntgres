package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumDiagnostics(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mis_diag(id INTEGER,m MONEY,iv INTERVAL,txt TEXT)", 0))
		run(fixture.Exec("INSERT INTO mis_diag VALUES (1,'1','1 day','text')", 1))
		missing := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguous := "Could not choose a best candidate function. You might need to add explicit type casts."
		misplaced := "No aggregate function matches the given name and argument types. Perhaps you misplaced ORDER BY; ORDER BY must appear after all regular arguments of the aggregate."
		for _, c := range []struct {
			q, code, message, hint, token string
			args                          []any
		}{
			{"SELECT sum() FROM mis_diag", "42883", "function sum() does not exist", missing, "sum", nil},
			{"SELECT sum(*) FROM mis_diag", "42883", "function sum() does not exist", missing, "sum", nil},
			{"SELECT sum(m,m) FROM mis_diag", "42883", "function sum(money, money) does not exist", missing, "sum", nil},
			{"SELECT sum(iv,iv) FROM mis_diag", "42883", "function sum(interval, interval) does not exist", missing, "sum", nil},
			{"SELECT sum(TRUE) FROM mis_diag", "42883", "function sum(boolean) does not exist", missing, "sum", nil},
			{"SELECT sum(txt) FROM mis_diag", "42883", "function sum(text) does not exist", missing, "sum", nil},
			{"SELECT sum(NULL) FROM mis_diag", "42725", "function sum(unknown) is not unique", ambiguous, "sum", nil},
			{"SELECT sum('1') FROM mis_diag", "42725", "function sum(unknown) is not unique", ambiguous, "sum", nil},
			{"SELECT sum($1) FROM mis_diag", "42725", "function sum(unknown) is not unique", ambiguous, "sum", []any{"1"}},
			{"SELECT public.sum(m) FROM mis_diag", "42883", "function public.sum(money) does not exist", missing, "public", nil},
			{"SELECT absent_mis_schema.sum(iv) FROM mis_diag", "3F000", "schema \"absent_mis_schema\" does not exist", "", "absent_mis_schema", nil},
			{"SELECT \"SUM\"(m) FROM mis_diag", "42883", "function SUM(money) does not exist", missing, "\"SUM\"", nil},
			{"SELECT sum(sum(m)) FROM mis_diag", "42803", "aggregate function calls cannot be nested", "", "sum(m)", nil},
			{"SELECT sum(sum(iv)) FROM mis_diag", "42803", "aggregate function calls cannot be nested", "", "sum(iv)", nil},
			{"SELECT id FROM mis_diag WHERE sum(m)='1'::money", "42803", "aggregate functions are not allowed in WHERE", "", "sum", nil},
			{"SELECT id FROM mis_diag GROUP BY sum(iv)", "42803", "aggregate functions are not allowed in GROUP BY", "", "sum", nil},
			{"SELECT id,sum(m) FROM mis_diag", "42803", "column \"mis_diag.id\" must appear in the GROUP BY clause or be used in an aggregate function", "", "id,", nil},
			{"UPDATE mis_diag SET m=sum(m)", "42803", "aggregate functions are not allowed in UPDATE", "", "sum", nil},
			{"INSERT INTO mis_diag(m) VALUES(sum('1'::money))", "42803", "aggregate functions are not allowed in VALUES", "", "sum", nil},
			{"INSERT INTO mis_diag(iv) VALUES(sum('1 day'::interval))", "42803", "aggregate functions are not allowed in VALUES", "", "sum", nil},
			{"UPDATE mis_diag SET iv='1 day' RETURNING sum(iv)", "42803", "aggregate functions are not allowed in RETURNING", "", "sum", nil},
			{"SELECT sum(absent,TRUE) FROM mis_diag", "42703", "column \"absent\" does not exist", "", "absent", nil},
			{"SELECT sum(m,m) FILTER(WHERE 1) FROM mis_diag", "42804", "argument of FILTER must be type boolean, not type integer", "", "1)", nil},
			{"SELECT sum(iv) FILTER(WHERE sum(m)>'0'::money) FROM mis_diag", "42803", "aggregate functions are not allowed in FILTER", "", "sum(m)", nil},
			{"SELECT sum(DISTINCT m ORDER BY id) FROM mis_diag", "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", "", "id)", nil},
			{"SELECT sum(m,m ORDER BY id,m) FROM mis_diag", "42883", "function sum(money, money) does not exist", misplaced, "sum", nil},
			{"SELECT sum(iv,iv ORDER BY id,iv) FROM mis_diag", "42883", "function sum(interval, interval) does not exist", misplaced, "sum", nil},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + c.q
				t.Run(q, func(t *testing.T) {
					position := utf8.RuneCountInString(q[:strings.Index(q, c.token)]) + 1
					fixture.IntervalAvgDiagnostic(t, p, conn, b.Recorder, q, fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint, Position: fixture.Position(position)}, c.args...)
				})
			}
		}
		for _, column := range []string{"m", "iv"} {
			kind := "money"
			if column == "iv" {
				kind = "interval"
			}
			for _, n := range []int{100, 101} {
				q := "SELECT sum(" + strings.TrimSuffix(strings.Repeat(column+",", n), ",") + ") FROM mis_diag"
				d := fixture.Diagnostic{Code: "42883", Message: "function sum(" + strings.TrimSuffix(strings.Repeat(kind+", ", n), ", ") + ") does not exist", Hint: missing, Position: "8"}
				if n == 101 {
					d = fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}
				}
				fixture.IntervalAvgDiagnostic(t, p, conn, b.Recorder, q, d)
				if n == 101 {
					q = "SELECT sum(" + strings.TrimSuffix(strings.Repeat(column+",", n), ",") + ") FILTER(WHERE 1) FROM mis_diag"
					fixture.IntervalAvgDiagnostic(t, p, conn, b.Recorder, q, fixture.Diagnostic{Code: "42804", Message: "argument of FILTER must be type boolean, not type integer", Position: fixture.Position(strings.Index(q, "1)") + 1)})
				}
			}
		}
		prefixFailure := "INSERT INTO mis_diag(id,m) VALUES (2,'2'),(3,sum('3'::money))"
		fixture.IntervalAvgDiagnostic(t, p, conn, b.Recorder, prefixFailure, fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in VALUES", Position: fixture.Position(strings.Index(prefixFailure, "sum") + 1)})
		run(fixture.Select("SELECT id,m,iv,txt FROM mis_diag", []string{"id", "m", "iv", "txt"}, []string{"INT4", "MONEY", "INTERVAL", "TEXT"}, [][]any{{int64(1), "$1.00", "1 day", "text"}}))
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumPair("SELECT pg_catalog.sum(m) AS m,pg_catalog.sum(iv) AS iv FROM mis_diag", [][]any{{"$1.00", "1 day"}}))
	})
}
