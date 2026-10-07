package smoke_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
)

func TestRowLockViewSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				db, a, b := rowLockSmokeFixture(t)
				for _, q := range []string{"INSERT INTO t VALUES(3,30)", "CREATE VIEW v AS SELECT id,v FROM t WHERE id<3", "CREATE VIEW w AS SELECT id,v+1 AS val FROM v"} {
					_, err := a.ExecContext(ctx, q)
					rowLockSmokeCheck(t, err)
				}
				holder, err := a.BeginTx(ctx, nil)
				rowLockSmokeCheck(t, err)
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
				defer holder.Rollback()
				rowLockSmokeCheck(t, transactionSelectTypedValues(ctx, holder, "SELECT n FROM grouped", prepared, "n", "INT8"))
				rowLockSmokeBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
				rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1))
				rowLockSmokeCheck(t, holder.Rollback())
				var dump bytes.Buffer
				rowLockSmokeCheck(t, stuntgres.Dump(ctx, db, &dump))
				if !strings.Contains(dump.String(), "FOR UPDATE") {
					t.Fatal("lost stored mode", dump.String())
				}
				rowLockSmokeCheck(t, stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())))
				holder, err = a.BeginTx(ctx, nil)
				rowLockSmokeCheck(t, err)
				defer holder.Rollback()
				rowLockSmokeCheck(t, transactionSelectValues(ctx, holder, "SELECT v FROM stored WHERE id=1 FOR KEY SHARE", prepared, 10))
				rowLockSmokeBlocked(t, b, "UPDATE t SET v=99 WHERE id=1", prepared)
				rowLockSmokeCheck(t, holder.Rollback())
				// Native catalogs have no durable physical tuple identity, unlike PostgreSQL.
				err = transactionSelectValues(ctx, b, "SELECT oid FROM pg_catalog.pg_class WHERE FALSE "+clause, prepared)
				var diagnostic *stuntgres.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "row locking is not supported for virtual catalog relations" || diagnostic.Position != "" || diagnostic.Detail != "" || diagnostic.Hint != "" {
					t.Fatal(err)
				}
				rowLockSmokeCheck(t, transactionSelectTypedValues(ctx, b, "SELECT val FROM w WHERE id=1", prepared, "val", "INT4", 11))
			})
		}
	}
}
