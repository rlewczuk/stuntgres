package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockIndependence(t *testing.T) {
	for _, change := range []string{"UPDATE t SET v=99 WHERE id=1", "DELETE FROM t WHERE id=1"} {
		for _, prepared := range []bool{false, true} {
			t.Run(change+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
						fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
						tx, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.RowAffected(t, p.Ctx, tx, change, prepared, 1)
						fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t WHERE id=1", prepared, backend.Recorder.RawBackend), 10)
						fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t WHERE id=2 FOR UPDATE", prepared, backend.Recorder.RawBackend), 20)
						fixture.RowAffected(t, p.Ctx, b, "DELETE FROM t WHERE id=2", prepared, 1)
						tx.Rollback()
						fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t ORDER BY v FOR SHARE", prepared, backend.Recorder.RawBackend), 10)
					})
				}
			})
		}
	}
}
