//go:build reference

package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitLockedCache(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"t", "own", "stored_rows"} {
			t.Run(source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewPair(t)
				for backend, db := range []*sql.DB{p.Postgres, p.Memory} {
					t.Run([]string{"postgres", "memory"}[backend], func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, db)
						for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20)", "CREATE SEQUENCE s", "CREATE VIEW own AS SELECT id,nextval('s') AS n FROM t WHERE id=1 ORDER BY n,n LIMIT 1 FOR UPDATE", "CREATE VIEW stored_rows AS SELECT id,v FROM t WHERE id=1 FOR UPDATE"} {
							if _, err := a.ExecContext(p.Ctx, q); err != nil {
								t.Fatal(q, err)
							}
						}
						q := "SELECT nextval('s') AS n FROM t WHERE id=1 ORDER BY n,n LIMIT 1 FOR UPDATE"
						want := int64(2)
						if source == "own" {
							q = "SELECT n FROM own"
						}
						if source == "stored_rows" {
							q = "SELECT nextval('s') AS n FROM stored_rows ORDER BY n,n LIMIT 1"
							want = 1
						}
						ap, bp := 0, 0
						if backend == 0 {
							ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
						}
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						fixture.RowValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false), 10)
						fixture.RowAffected(t, p.Ctx, holder, "UPDATE t SET v=30 WHERE id=1", false, 1)
						done := fixture.RowAsync(t, p.Ctx, b, q, prepared, true)
						if backend == 0 {
							fixture.RowWait(t, p.Ctx, bp, ap)
						} else if source != "stored_rows" {
							fixture.LimitSequenceCapture(t, p.Ctx, holder)
						}
						if err := holder.Commit(); err != nil {
							t.Fatal(err)
						}
						fixture.ViewValues(t, fixture.LockResult(t, done), "n", "INT8", want)
						fixture.ViewValues(t, fixture.RowQuery(p.Ctx, b, "SELECT currval('s')", false), "currval", "INT8", want)
					})
				}
			})
		}
	}
}
