package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectScalarSource(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE s(i INT,v INT,b BOOL)", 0), fixture.Exec("INSERT INTO s VALUES(1,NULL,TRUE),(2,3,FALSE)", 2), fixture.Exec("CREATE TABLE t(i BIGINT,b BOOL)", 0)} {
				run(s)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("INSERT INTO t SELECT num_nulls(v),NOT b FROM s WHERE i>0 ORDER BY i", 2)
				s.Prepared = prepared
				run(s)
			}
			result := fixture.Select("SELECT * FROM t", []string{"i", "b"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}, {int64(0), true}, {int64(1), false}, {int64(0), true}})
			result.Bag = true
			run(result)
			run(fixture.Exec("INSERT INTO t SELECT num_nulls(v),NOT b FROM s WHERE FALSE", 0))
			run(result)
		})
	}
}
