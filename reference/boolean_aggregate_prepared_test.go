package reference_test

import "testing"

func TestReferenceBooleanAggregateHeldPrepared(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(b BOOL)", 0))
			run(referenceExec("INSERT INTO t VALUES(TRUE),(NULL)", 2))
			const q = "SELECT bool_and(b),bool_or(b),every(b) FROM t"
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			check := func(want [][]any) {
				referenceRunStep(t, p.ctx, referenceHeldStatement{conn, stmt, q}, backend.recorder, referenceSelect(q, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, want))
			}
			check([][]any{{true, true, true}})
			run(referenceExec("INSERT INTO t VALUES(FALSE)", 1))
			check([][]any{{false, true, false}})
			run(referenceExec("CREATE SEQUENCE s", 0))
			step := referenceSelect("SELECT bool_and(nextval('s')>0),every(nextval('s')>0),bool_or(nextval('s')>0) FROM t", []string{"bool_and", "every", "bool_or"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
			run(step)
			run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(9)}}))
			run(referencePrepared(step))
			run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(18)}}))
		})
	}
}
