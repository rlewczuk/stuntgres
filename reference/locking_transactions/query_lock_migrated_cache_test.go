package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRowLockMigratedOrderedCacheSmoke(t *testing.T) {

	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"t", "w", "stored"} {
			t.Run(source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {

						transactionAffected := queryLockMigratedAffected
						transactionSelectTypedValues := func(ctx context.Context, x fixture.Executor, q string, prepared bool, name, typ string, want ...int64) error {
							return queryLockMigratedTyped(ctx, x, q, prepared, backend.PG, name, typ, want...)
						}
						rowLockSmokeCheck := func(t *testing.T, err error) {
							t.Helper()
							if err != nil {
								t.Fatal(err)
							}
						}
						rowLockSmokeBlocked := func(t *testing.T, x fixture.Executor, q string, prepared bool) {
							queryLockMigratedBlocked(t, p.Ctx, x, backend.PG, q, prepared)
						}
						rowLockSmokeFixture := func(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn) {
							a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
							fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
							fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
							return backend.DB, a, b
						}
						ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
						defer cancel()
						_, a, b := rowLockSmokeFixture(t)
						for _, q := range []string{"CREATE VIEW w AS SELECT id,v+1 AS val FROM t ORDER BY val,id", "CREATE VIEW stored AS SELECT id,v FROM t ORDER BY v+1,id FOR UPDATE", "CREATE SEQUENCE s"} {
							_, err := a.ExecContext(ctx, q)
							rowLockSmokeCheck(t, err)
						}
						expression := "v+1"
						want := []int64{21, 11}
						if source == "w" {
							expression = "val+1"
							want = []int64{22, 12}
						}
						query := "SELECT " + expression + " AS k FROM " + source + " ORDER BY k DESC NULLS LAST,id ASC"
						if source != "stored" {
							query += " FOR UPDATE"
						}
						holder, err := a.BeginTx(ctx, nil)
						rowLockSmokeCheck(t, err)
						fixture.QueryLockRollbackCleanup(t, holder)
						defer holder.Rollback()
						rowLockSmokeCheck(t, transactionSelectTypedValues(ctx, holder, query, prepared, "k", "INT4", want...))
						rowLockSmokeBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
						rowLockSmokeCheck(t, holder.Rollback())
						query = "SELECT nextval('s') AS n FROM " + source + " WHERE id=1 ORDER BY n,n"
						if source != "stored" {
							query += " FOR UPDATE"
						}
						rowLockSmokeCheck(t, transactionSelectTypedValues(ctx, a, query, prepared, "n", "INT8", 1))
						rowLockSmokeCheck(t, transactionSelectTypedValues(ctx, a, "SELECT currval('s') AS n", false, "n", "INT8", 1))
						rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=v WHERE id=1", prepared, 1))
					})
				}
			})
		}
	}

}
