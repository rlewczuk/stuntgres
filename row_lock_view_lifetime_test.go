package stuntgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
)

func TestRowLockViewLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db, a, b, ctx := rowLockPublicViewFixture(t)
			// Implicit eager locking releases before the caller consumes its retained rows.
			rows, closeQuery, err := transactionQuery(ctx, a, "SELECT val FROM w ORDER BY val FOR UPDATE", prepared)
			if err != nil {
				t.Fatal(err)
			}
			defer closeQuery()
			transactionAffected(t, ctx, b, "UPDATE t SET v=20 WHERE id=1", prepared, 1)
			transactionValues(t, rows, 11, 21)
			closeQuery()
			transactionAffected(t, ctx, a, "CREATE TABLE target(v INT)", false, 0)
			transactionAffected(t, ctx, a, "UPDATE t SET v=10 WHERE id=1", false, 1)
			_, err = transactionExec(ctx, a, "INSERT INTO target SELECT 1/(val-11) FROM w FOR UPDATE", prepared)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
			transactionSelect(t, ctx, b, "SELECT v FROM target", false)
			transactionAffected(t, ctx, b, "UPDATE t SET v=v WHERE id=1", false, 1)
			// Cancelled public boundaries use identity, never invented SQLSTATE/enqueue proof.
			holder, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			transactionSelect(t, ctx, holder, "SELECT val FROM w WHERE id=1 FOR UPDATE", prepared, 11)
			bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			defer cancel()
			blocked, closeBlocked, err := transactionQuery(bounded, b, "SELECT val FROM w WHERE id=1 FOR SHARE", prepared)
			if closeBlocked != nil {
				closeBlocked()
			}
			if blocked != nil {
				blocked.Close()
				t.Fatal("cancelled cursor")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			var diagnostic *stuntgres.Error
			if errors.As(err, &diagnostic) {
				t.Fatal("native deadline SQLSTATE", diagnostic)
			}
			holder.Rollback()
			rows, closeQuery, err = transactionQuery(ctx, b, "SELECT val FROM w ORDER BY val FOR KEY SHARE", prepared)
			if err != nil {
				t.Fatal(err)
			}
			defer closeQuery()
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(id INT,v INT);INSERT INTO t VALUES(1,99);CREATE VIEW w AS SELECT id,v+1 AS val FROM t")); err != nil {
				t.Fatal(err)
			}
			transactionValues(t, rows, 11, 21)
			closeQuery()
			transactionSelect(t, ctx, a, "SELECT val FROM w FOR UPDATE", prepared, 100)
		})
	}
}
