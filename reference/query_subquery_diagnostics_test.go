package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceQuerySubqueryDiagnostics(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
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
							want := controlsReferenceDiagnostic("42601", tc.message)
							want.Position = referencePosition(pos)
							s := referenceErrorDiagnostic(q, want)
							s.selectRows, s.prepared = queryMode, prepared
							if prepared {
								s.errorStage = "prepare"
							} else {
								s.errorStage = "execute"
							}
							subqueryRun(t, p, b, c)(s)
						})
					}
				}
			}
		}
		for _, q := range []string{"SELECT EXISTS(SELECT)", "SELECT /*é*/ EXISTS(SELECT)"} {
			subqueryBoth(run, referenceSelect(q, []string{"exists"}, []string{"BOOL"}, [][]any{{true}}))
			subqueryBoth(run, referenceExec(q, 1))
		}
		for _, q := range []string{"SELECT (SELECT v FROM r WHERE v=2)", "SELECT /*é*/ (SELECT v FROM r WHERE v=2)"} {
			run(referenceErrorDiagnostic(q, subqueryCardinality()))
			run(referencePrepared(referenceErrorDiagnostic(q, subqueryCardinality())))
			subqueryQueryFailure(t, p, b, c, q, subqueryCardinality())
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal("cardinality became data-dependent Prepare failure", err)
			}
			subqueryQueryFailure(t, p, b, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, q, subqueryCardinality())
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
			want := controlsReferenceDiagnostic(tc.code, tc.message)
			want.Position, want.Hint = tc.position, tc.hint
			for _, prepared := range []bool{false, true} {
				for _, queryMode := range []bool{false, true} {
					s := referenceErrorDiagnostic(tc.q, want)
					s.selectRows, s.prepared = queryMode, prepared
					if prepared {
						s.errorStage = "prepare"
					} else {
						s.errorStage = "execute"
					}
					run(s)
				}
			}
		}
		// Whole-input validation: a legal prefix must not publish its write.
		q := "INSERT INTO r VALUES(99); SELECT (SELECT 1"
		want := controlsReferenceDiagnostic("42601", "syntax error at end of input")
		want.Position = referencePosition(len(q) + 1)
		subqueryBoth(run, referenceErrorDiagnostic(q, want))
		run(referenceSelect("SELECT v FROM r ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {nil}}))
	})
}
