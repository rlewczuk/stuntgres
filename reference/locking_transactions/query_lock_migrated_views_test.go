package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRowLockMigratedViewSmoke(t *testing.T) {

	for _, prepared := range []bool{false, true} {
		for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {

						transactionAffected := queryLockMigratedAffected
						transactionSelectTypedValues := func(ctx context.Context, x fixture.Executor, q string, prepared bool, name, typ string, want ...int64) error {
							return queryLockMigratedTyped(ctx, x, q, prepared, backend.PG, name, typ, want...)
						}
						transactionSelectValues := func(ctx context.Context, x fixture.Executor, q string, prepared bool, want ...int64) error {
							return queryLockMigratedTyped(ctx, x, q, prepared, backend.PG, "v", "INT4", want...)
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
						for _, q := range []string{"INSERT INTO t VALUES(3,30)", "CREATE VIEW v AS SELECT id,v FROM t WHERE id<3", "CREATE VIEW w AS SELECT id,v+1 AS val FROM v"} {
							_, err := a.ExecContext(ctx, q)
							rowLockSmokeCheck(t, err)
						}
						holder, err := a.BeginTx(ctx, nil)
						rowLockSmokeCheck(t, err)
						fixture.QueryLockRollbackCleanup(t, holder)
						defer holder.Rollback()
						rowLockSmokeCheck(t, transactionSelectTypedValues(ctx, holder, "SELECT val FROM w WHERE id=1 "+clause, prepared, "val", "INT4", 11))
						rowLockSmokeCheck(t, transactionSelectTypedValues(ctx, b, "SELECT val FROM w WHERE id=1", prepared, "val", "INT4", 11))
						rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1))
						rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=v WHERE id=3", prepared, 1))
						if clause == "FOR KEY SHARE" {
							rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=v WHERE id=1", prepared, 1))
						} else {
							rowLockSmokeBlocked(t, b, "UPDATE t SET v=99 WHERE id=1", prepared)
						}
						rowLockSmokeBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
						rowLockSmokeCheck(t, holder.Rollback())
						_, err = a.ExecContext(ctx, "CREATE VIEW stored AS SELECT id,v FROM t ORDER BY id FOR UPDATE")
						rowLockSmokeCheck(t, err)
						_, err = a.ExecContext(ctx, "CREATE VIEW grouped AS SELECT count(*) AS n FROM stored WHERE id=1 HAVING count(*)=0")
						rowLockSmokeCheck(t, err)
						holder, err = a.BeginTx(ctx, nil)
						rowLockSmokeCheck(t, err)
						fixture.QueryLockRollbackCleanup(t, holder)
						defer holder.Rollback()
						rowLockSmokeCheck(t, transactionSelectTypedValues(ctx, holder, "SELECT n FROM grouped", prepared, "n", "INT8"))
						rowLockSmokeBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
						rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1))
						rowLockSmokeCheck(t, holder.Rollback())
						if !backend.PG {

							var dump bytes.Buffer
							rowLockSmokeCheck(t, stuntgres.Dump(ctx, p.Native, &dump))
							if !strings.Contains(dump.String(), "FOR UPDATE") {
								t.Fatal("lost stored mode", dump.String())
							}
							rowLockSmokeCheck(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())))
						}
						holder, err = a.BeginTx(ctx, nil)
						rowLockSmokeCheck(t, err)
						fixture.QueryLockRollbackCleanup(t, holder)
						defer holder.Rollback()
						rowLockSmokeCheck(t, transactionSelectValues(ctx, holder, "SELECT v FROM stored WHERE id=1 FOR KEY SHARE", prepared, 10))
						rowLockSmokeBlocked(t, b, "UPDATE t SET v=99 WHERE id=1", prepared)
						rowLockSmokeCheck(t, holder.Rollback())
						if !backend.PG {

							// Native catalogs have no durable physical tuple identity, unlike PostgreSQL.
							err = transactionSelectValues(ctx, b, "SELECT oid FROM pg_catalog.pg_class WHERE FALSE "+clause, prepared)
							var diagnostic *stuntgres.Error
							if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "row locking is not supported for virtual catalog relations" || diagnostic.Position != "" || diagnostic.Detail != "" || diagnostic.Hint != "" {
								t.Fatal(err)
							}
						}
						rowLockSmokeCheck(t, transactionSelectTypedValues(ctx, b, "SELECT val FROM w WHERE id=1", prepared, "val", "INT4", 11))
					})
				}
			})
		}
	}

}
