package reference_test

import "testing"

func TestReferenceQueryAggregateHavingInsertConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(k TEXT,v INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3))
	p.step(t, referenceExec("CREATE TABLE out(k TEXT,n BIGINT)", 0))
	for _, backend := range p.backends() {
		func() {
			const q = "SELECT count(*) FROM t"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held, err := stmt.QueryContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES('b',3)", 1))
			queryCountHeldRows(t, held, 3)
			referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		}()
	}
	for _, prepared := range []bool{false, true} {
		step := referenceExec("INSERT INTO out SELECT k,count(*) FROM t GROUP BY k HAVING count(*)>1", 1)
		step.prepared = prepared
		p.step(t, step)
	}
	out := referenceSelect("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {"a", int64(2)}})
	out.bag = true
	p.step(t, out)
	p.step(t, referencePrepared(out))
}
