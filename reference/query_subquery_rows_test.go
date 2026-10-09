package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQuerySubqueryRows(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		subqueryReductionFixture(run)
		for _, constructor := range []string{"ROW", ""} {
			for _, tc := range []struct {
				tail  string
				value any
			}{
				{"(1,CAST(NULL AS INT)) IN (SELECT 1,2)", nil},
				{"(1,CAST(NULL AS INT)) IN (SELECT 2,2)", false},
				{"(1,CAST(NULL AS INT)) NOT IN (SELECT 1,2)", nil},
				{"(1,CAST(NULL AS INT)) NOT IN (SELECT 2,2)", true},
				{"(1,2) IN (SELECT 1,2)", true}, {"(1,2) NOT IN (SELECT 1,2)", false},
				{"(1,2) = ANY(SELECT 1,2)", true}, {"(1,2) = SOME(SELECT 1,2)", true},
				{"(1,2) <> ALL(SELECT 1,2)", false},
				{"(1,2) = ANY(SELECT 1,2 WHERE FALSE)", false},
				{"(1,2) <> ALL(SELECT 1,2 WHERE FALSE)", true},
				{"(1,2) = ANY(SELECT v,2 FROM r)", true},
				{"(3,2) = ANY(SELECT v,2 FROM r)", nil},
				{"(3,2) <> ALL(SELECT v,2 FROM r)", nil},
				{"(1,2,CAST(NULL AS INT)) < (SELECT 1,3,0)", true},
				{"(1,2,CAST(NULL AS INT)) > (SELECT 1,3,0)", false},
				{"(1,CAST(NULL AS INT),0) < (SELECT 1,3,1)", nil},
				{"(CAST(NULL AS INT),1) = (SELECT 2,2)", false},
				{"(1,2) = (SELECT 1,2)", true}, {"(1,2) <> (SELECT 1,2)", false},
				{"(1,2) < (SELECT 1,2)", false}, {"(1,2) > (SELECT 1,2)", false},
				{"(1,2) <= (SELECT 1,2)", true}, {"(1,2) >= (SELECT 1,2)", true},
				{"(1,2) = (SELECT 1,2 WHERE FALSE)", nil},
			} {
				t.Run(constructor+tc.tail, func(t *testing.T) {
					subqueryBoth(subqueryRun(t, p, b, c), referenceSelect("SELECT "+constructor+tc.tail+" AS answer", []string{"answer"}, []string{"BOOL"}, [][]any{{tc.value}}))
				})
			}
		}
		for _, q := range []string{"SELECT ROW(1,2) = (SELECT 1,2 FROM r WHERE v=2)", "SELECT (1,2) = (SELECT 1,2 FROM r WHERE v=2)"} {
			subqueryQueryFailure(t, p, b, c, q, subqueryCardinality())
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			subqueryQueryFailure(t, p, b, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, q, subqueryCardinality())
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			q     string
			value any
		}{
			{"SELECT (SELECT v FROM r WHERE v=1)", int64(1)},
			{"SELECT (SELECT v FROM r WHERE FALSE)", nil},
			{"SELECT (SELECT v FROM r WHERE v IS NULL)", nil},
		} {
			subqueryBoth(run, referenceSelect(tc.q, []string{"v"}, []string{"INT4"}, [][]any{{tc.value}}))
		}
		subqueryBoth(run, referenceSelect("SELECT (SELECT v FROM r WHERE FALSE) AS value WHERE FALSE", []string{"value"}, []string{"INT4"}, nil))
		// Small explicit rows have no implicit-row equivalent; no public record export.
		zero := controlsReferenceDiagnostic("0A000", "cannot compare rows of zero length")
		zero.Position = "14"
		for _, prepared := range []bool{false, true} {
			for _, queryMode := range []bool{false, true} {
				s := referenceErrorDiagnostic("SELECT ROW() = (SELECT) AS zero", zero)
				s.prepared, s.selectRows = prepared, queryMode
				if prepared {
					s.errorStage = "prepare"
				} else {
					s.errorStage = "execute"
				}
				run(s)
			}
		}
		subqueryBoth(run, referenceSelect("SELECT ROW(1) = (SELECT 1) AS one", []string{"one"}, []string{"BOOL"}, [][]any{{true}}))
		// IN retains its released bare-label route while '(' continues a predicate.
		subqueryBoth(run, referenceSelect("SELECT 1 in", []string{"in"}, []string{"INT4"}, [][]any{{int64(1)}}))
		subqueryBoth(run, referenceSelect("SELECT (1 IN (SELECT 1)) in", []string{"in"}, []string{"BOOL"}, [][]any{{true}}))
		subqueryBoth(run, referenceSelect("SELECT 1 IN (SELECT 1) AS in", []string{"in"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
