package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceRowLockControls(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(v INT)", 0)
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(10),(20)", 2)
					tx, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					referenceLimitLockValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t ORDER BY v FOR SHARE", false), backend.pg, 10, 20)
					referenceLimitLockValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t ORDER BY v", false), backend.pg, 10, 20)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					db := backend.db
					if !backend.pg {
						db = p.native
					}
					rows, err := db.QueryContext(p.ctx, "SELECT v FROM t ORDER BY v FOR SHARE")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					if !backend.pg {
						if restore {
							err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(99)"))
						} else {
							err = stuntgres.Reset(p.ctx, p.native)
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 10, 20)
					if backend.pg {
						referenceLimitLockValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t ORDER BY v FOR UPDATE", true), true, 10, 20)
					} else if restore {
						referenceLimitLockValues(t, referenceRowQuery(p.ctx, p.native, "SELECT v FROM t FOR UPDATE", true), false, 99)
					}
				})
			}
		})
	}
}
