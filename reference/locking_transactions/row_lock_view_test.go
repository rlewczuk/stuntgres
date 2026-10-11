package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewFailingFirst(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2), fixture.Exec("CREATE VIEW v AS SELECT id,v FROM t", 0)} {
		p.Step(t, step)
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			for _, clause := range fixture.RowClauses {
				for _, prepared := range []bool{false, true} {
					t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						step := fixture.Select("SELECT v FROM v WHERE id=1 "+clause, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, a, backend.Recorder, step)
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
						fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					})
				}
			}
		})
	}
}
