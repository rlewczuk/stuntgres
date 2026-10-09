package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryDistinctSmoke(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctSetup(t, run)
		queryDistinctBoth(run, queryDistinctOrdered())
		// Preserve actual plain DB.Query/Prepare/Stmt.Query in addition to the
		// pinned Context routes and real held execution frames.
		s := queryDistinctOrdered()
		s.nonContext = true
		for _, prepared := range []bool{false, true} {
			s.prepared = prepared
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
		stmt, err := b.db.Prepare(s.query)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			for range 2 {
				rows, err := stmt.Query()
				stringCoreRetainedRows(t, rows, err, s.columns, s.types, s.rows, false)
			}
		}()
		queryDistinctHeldFrames(t, p, b, c)
		s = referenceSelect("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(1), int64(20)}, {nil, int64(10)}, {nil, int64(10)}, {nil, nil}, {nil, nil}})
		s.bag = true
		run(s)
		q := "SELECT DISTINCT a FROM t ORDER BY b"
		queryDistinctAnalysis(t, p, b, c, q, referenceDiagnostic{Code: "42P10", Message: "for SELECT DISTINCT, ORDER BY expressions must appear in select list", Position: queryDistinctPosition(q, "b")})
		queryDistinctArrayWitness(t, run)
		queryDistinctConsumerWitness(t, p, b, c, run)
	})
}
