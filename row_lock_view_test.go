package stuntgres_test

import (
	"context"
	"testing"
)

func TestRowLockViewFailingFirst(t *testing.T) {
	for _, clause := range rowLockPublicClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, b := transactionConnections(t)
				ctx := context.Background()
				transactionAffected(t, ctx, a, "CREATE TABLE t(id INT,v INT)", false, 0)
				transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", false, 2)
				transactionAffected(t, ctx, a, "CREATE VIEW v AS SELECT id,v FROM t", false, 0)
				holder, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				transactionSelect(t, ctx, holder, "SELECT v FROM v WHERE id=1 "+clause, prepared, 10)
				transactionSelect(t, ctx, b, "SELECT v FROM t WHERE id=1", false, 10)
			})
		}
	}
}
