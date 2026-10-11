package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryAggregateHavingInsertConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3))
	p.Step(t, fixture.Exec("CREATE TABLE out(k TEXT,n BIGINT)", 0))
	for _, backend := range p.Backends() {
		func() {
			const q = "SELECT count(*) FROM t"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES('b',3)", 1))
			queryCountHeldRows(t, held, 3)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, q}, backend.Recorder, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		}()
	}
	for _, prepared := range []bool{false, true} {
		step := fixture.Exec("INSERT INTO out SELECT k,count(*) FROM t GROUP BY k HAVING count(*)>1", 1)
		step.Prepared = prepared
		p.Step(t, step)
	}
	out := fixture.Select("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {"a", int64(2)}})
	out.Bag = true
	p.Step(t, out)
	p.Step(t, fixture.Prepared(out))
}
