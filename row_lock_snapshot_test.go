package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestRowLockKeyShareSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "CREATE TABLE t(id INT,v INT)", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1,10)", false, 1)
			reader, err := b.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Rollback()
			transactionSelect(t, ctx, reader, "SELECT v FROM t WHERE id=1", false, 10)
			transactionAffected(t, ctx, a, "UPDATE t SET v=20 WHERE id=1", false, 1)
			transactionSelect(t, ctx, reader, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", prepared, 10)
			transactionSelect(t, ctx, reader, "SELECT v FROM t WHERE v=10 FOR KEY SHARE", prepared, 10)
			transactionSelect(t, ctx, reader, "SELECT a.v FROM t a JOIN t b USING(id) WHERE a.id=1 AND b.v=10 FOR KEY SHARE", prepared, 10)
			transactionSelect(t, ctx, a, "SELECT v FROM t WHERE id=1", false, 20)
		})
	}
}

func TestRowLockKeyShareDeletedReplacement(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "CREATE TABLE t(id INT,v INT)", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1,10)", false, 1)
			reader, err := b.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Rollback()
			transactionSelect(t, ctx, reader, "SELECT v FROM t WHERE id=1", false, 10)
			transactionAffected(t, ctx, a, "UPDATE t SET v=20 WHERE id=1", false, 1)
			transactionAffected(t, ctx, a, "DELETE FROM t WHERE id=1", false, 1)
			rows, closeQuery, err := transactionQuery(ctx, reader, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", prepared)
			if closeQuery != nil {
				defer closeQuery()
			}
			if rows != nil {
				rows.Close()
				t.Fatal("deleted RR chain exposed rows")
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "40001", Message: "could not serialize access due to concurrent update"})
		})
	}
}
