package smoke_test

import (
	"context"
	"testing"
	"time"
)

func TestRowLockOrderedCacheSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"t", "w", "stored"} {
			t.Run(source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	}
}
