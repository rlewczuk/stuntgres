package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBooleanAggregateHeldPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(b BOOL)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(TRUE),(NULL)", 2))
			const q = "SELECT bool_and(b),bool_or(b),every(b) FROM t"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			check := func(want [][]any) {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, stmt, q}, backend.Recorder, fixture.Select(q, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, want))
			}
			check([][]any{{true, true, true}})
			run(fixture.Exec("INSERT INTO t VALUES(FALSE)", 1))
			check([][]any{{false, true, false}})
			run(fixture.Exec("CREATE SEQUENCE s", 0))
			step := fixture.Select("SELECT bool_and(nextval('s')>0),every(nextval('s')>0),bool_or(nextval('s')>0) FROM t", []string{"bool_and", "every", "bool_or"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
			run(step)
			run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(9)}}))
			run(fixture.Prepared(step))
			run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(18)}}))
		})
	}
}
