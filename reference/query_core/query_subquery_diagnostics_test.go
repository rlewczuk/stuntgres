package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySubqueryDiagnostics(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		subqueryReductionFixture(run)
		for _, tc := range []struct {
			q, message string
			pos        int
		}{
			{"SELECT (SELECT 1,2)", "subquery must return only one column", 8},
			{"SELECT 1 IN (SELECT 1,2)", "subquery has too many columns", 10},
			{"SELECT 1 NOT IN (SELECT 1,2)", "subquery has too many columns", 10},
			{"SELECT 1 = ANY (SELECT 1,2)", "subquery has too many columns", 10},
			{"SELECT ROW(1,2) = (SELECT 1)", "subquery has too few columns", 17},
			{"SELECT (SELECT 1", "syntax error at end of input", 17},
		} {
			for _, utf8 := range []bool{false, true} {
				q, pos := tc.q, tc.pos
				if utf8 {
					q = strings.Replace(q, "SELECT ", "SELECT /*é*/ ", 1)
					pos += 6
				}
				for _, prepared := range []bool{false, true} {
					for _, queryMode := range []bool{false, true} {
						t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+map[bool]string{false: "/exec", true: "/query"}[queryMode], func(t *testing.T) {
							want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
							want.Position = fixture.Position(pos)
							s := fixture.ErrorDiagnostic(q, want)
							s.SelectRows, s.Prepared = queryMode, prepared
							if prepared {
								s.ErrorStage = "prepare"
							} else {
								s.ErrorStage = "execute"
							}
							subqueryRun(t, p, b, c)(s)
						})
					}
				}
			}
		}
		for _, q := range []string{"SELECT EXISTS(SELECT)", "SELECT /*é*/ EXISTS(SELECT)"} {
			subqueryBoth(run, fixture.Select(q, []string{"exists"}, []string{"BOOL"}, [][]any{{true}}))
			subqueryBoth(run, fixture.Exec(q, 1))
		}
		for _, q := range []string{"SELECT (SELECT v FROM r WHERE v=2)", "SELECT /*é*/ (SELECT v FROM r WHERE v=2)"} {
			run(fixture.ErrorDiagnostic(q, subqueryCardinality()))
			run(fixture.Prepared(fixture.ErrorDiagnostic(q, subqueryCardinality())))
			subqueryQueryFailure(t, p, b, c, q, subqueryCardinality())
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("cardinality became data-dependent Prepare failure", err)
			}
			subqueryQueryFailure(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, subqueryCardinality())
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		// Full logical records observed in root's pinned18.0 tests-1/artifact17.
		for _, tc := range []struct{ q, code, message, position, hint string }{
			{"SELECT (SELECT FROM r)", "42601", "subquery must return only one column", "8", ""},
			{"SELECT ROW(1,2) = ANY(SELECT 1)", "42601", "subquery has too few columns", "17", ""},
			{"SELECT ROW(1,2) = ALL(SELECT 1,2,3)", "42601", "subquery has too many columns", "17", ""},
			{"SELECT 1 = ANY(SELECT TRUE)", "42883", "operator does not exist: integer = boolean", "10", "No operator matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT EXISTS(SELECT missing FROM r WHERE FALSE) WHERE FALSE", "42703", `column "missing" does not exist`, "22", ""},
			{"SELECT (SELECT missing FROM r WHERE FALSE)", "42703", `column "missing" does not exist`, "16", ""},
			{"SELECT 1 IN (SELECT v,v FROM r WHERE FALSE) WHERE FALSE", "42601", "subquery has too many columns", "10", ""},
		} {
			want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			want.Position, want.Hint = tc.position, tc.hint
			for _, prepared := range []bool{false, true} {
				for _, queryMode := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.q, want)
					s.SelectRows, s.Prepared = queryMode, prepared
					if prepared {
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					run(s)
				}
			}
		}
		// Whole-input validation: a legal prefix must not publish its write.
		q := "INSERT INTO r VALUES(99); SELECT (SELECT 1"
		want := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
		want.Position = fixture.Position(len(q) + 1)
		subqueryBoth(run, fixture.ErrorDiagnostic(q, want))
		run(fixture.Select("SELECT v FROM r ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {nil}}))
	})
}
