package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockControls(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(v INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(10),(20)", 2)
					tx, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, tx, "SELECT v FROM t ORDER BY v FOR SHARE", false), backend.PG, 10, 20)
					fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t ORDER BY v", false), backend.PG, 10, 20)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					db := backend.DB
					if !backend.PG {
						db = p.Native
					}
					rows, err := db.QueryContext(p.Ctx, "SELECT v FROM t ORDER BY v FOR SHARE")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					if !backend.PG {
						if restore {
							err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(99)"))
						} else {
							err = stuntgres.Reset(p.Ctx, p.Native)
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 10, 20)
					if backend.PG {
						fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t ORDER BY v FOR UPDATE", true), true, 10, 20)
					} else if restore {
						fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, p.Native, "SELECT v FROM t FOR UPDATE", true), false, 99)
					}
				})
			}
		})
	}
}
