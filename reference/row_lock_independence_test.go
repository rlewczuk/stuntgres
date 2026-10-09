package reference_test

import (
	"testing"
)

func TestReferenceRowLockIndependence(t *testing.T) {
	for _, change := range []string{"UPDATE t SET v=99 WHERE id=1", "DELETE FROM t WHERE id=1"} {
		for _, prepared := range []bool{false, true} {
			t.Run(change+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
						referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
						tx, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						referenceRowAffected(t, p.ctx, tx, change, prepared, 1)
						referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE id=1", prepared, backend.recorder.rawBackend), 10)
						referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE id=2 FOR UPDATE", prepared, backend.recorder.rawBackend), 20)
						referenceRowAffected(t, p.ctx, b, "DELETE FROM t WHERE id=2", prepared, 1)
						tx.Rollback()
						referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t ORDER BY v FOR SHARE", prepared, backend.recorder.rawBackend), 10)
					})
				}
			})
		}
	}
}
