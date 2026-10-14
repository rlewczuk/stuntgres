package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumQuotedFieldFallback(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`CREATE TYPE mis_sum_projection AS ("SUM" MONEY,"Sum" INTERVAL)`, 0))
		for _, c := range []struct {
			query, name, kind string
			want              any
		}{
			{`SELECT "SUM"(ROW('12.34'::money,'1 mon 30 days'::interval)::mis_sum_projection) AS m`, "m", "MONEY", "$12.34"},
			{`SELECT "Sum"(ROW('12.34'::money,'1 mon 30 days'::interval)::mis_sum_projection) AS iv`, "iv", "INTERVAL", "1 mon 30 days"},
			{`SELECT "SUM"(NULL::mis_sum_projection) AS m`, "m", "MONEY", nil},
			{`SELECT "Sum"(NULL::mis_sum_projection) AS iv`, "iv", "INTERVAL", nil},
		} {
			moneyIntervalSumBoth(t, p, conn, b, fixture.Select(c.query, []string{c.name}, []string{c.kind}, [][]any{{c.want}}))
		}
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		fixture.IntervalAvgDiagnostic(t, p, conn, b.Recorder, `SELECT "SUM"('1 day'::interval)`, fixture.Diagnostic{Code: "42883", Message: "function SUM(interval) does not exist", Hint: hint, Position: "8"})
	})
}

func TestReferenceMoneyIntervalSumQuotedArity(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE mis_quoted_arity(m MONEY,iv INTERVAL)", 0))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, name := range []string{"SUM", "Sum"} {
			for _, input := range []struct{ column, kind string }{{"m", "money"}, {"iv", "interval"}} {
				for _, n := range []int{100, 101} {
					for _, prefix := range []string{"", "/*é*/ "} {
						q := prefix + `SELECT "` + name + `"(` + strings.TrimSuffix(strings.Repeat(input.column+",", n), ",") + ") FROM mis_quoted_arity"
						position := utf8.RuneCountInString(q[:strings.Index(q, `"`+name+`"`)]) + 1
						want := fixture.Diagnostic{
							Code:     "42883",
							Message:  "function " + name + "(" + strings.TrimSuffix(strings.Repeat(input.kind+", ", n), ", ") + ") does not exist",
							Hint:     hint,
							Position: fixture.Position(position),
						}
						if n == 101 {
							want = fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: fixture.Position(position)}
						}
						t.Run(name+"/"+input.column+"/"+fixture.Position(n)+prefix, func(t *testing.T) {
							fixture.IntervalAvgDiagnostic(t, p, conn, b.Recorder, q, want)
						})
					}
				}
				// Transform every child before the global limit: a missing last
				// argument must win even when the preceding 100 are valid.
				q := `SELECT "` + name + `"(` + strings.Repeat(input.column+",", 100) + "absent) FROM mis_quoted_arity"
				fixture.IntervalAvgDiagnostic(t, p, conn, b.Recorder, q, fixture.Diagnostic{Code: "42703", Message: `column "absent" does not exist`, Position: fixture.Position(strings.Index(q, "absent") + 1)})
			}
		}
	})
}
