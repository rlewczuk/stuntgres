package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctSmoke(t *testing.T) {
	fixture.QueryDistinctScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		fixture.QueryDistinctSetup(t, run)
		fixture.QueryDistinctBoth(run, fixture.QueryDistinctOrdered())
		// Preserve actual plain DB.Query/Prepare/Stmt.Query in addition to the
		// pinned Context routes and real held execution frames.
		s := fixture.QueryDistinctOrdered()
		s.NonContext = true
		for _, prepared := range []bool{false, true} {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		stmt, err := b.DB.Prepare(s.Query)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			for range 2 {
				rows, err := stmt.Query()
				fixture.StringCoreRetainedRows(t, rows, err, s.Columns, s.Types, s.Rows, false)
			}
		}()
		queryDistinctHeldFrames(t, p, b, c)
		s = fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(1), int64(20)}, {nil, int64(10)}, {nil, int64(10)}, {nil, nil}, {nil, nil}})
		s.Bag = true
		run(s)
		q := "SELECT DISTINCT a FROM t ORDER BY b"
		fixture.QueryDistinctAnalysis(t, p, b, c, q, fixture.Diagnostic{Code: "42P10", Message: "for SELECT DISTINCT, ORDER BY expressions must appear in select list", Position: queryDistinctPosition(q, "b")})
		queryDistinctArrayWitness(t, run)
		queryDistinctConsumerWitness(t, p, b, c, run)
	})
}
