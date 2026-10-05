package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestTransactionAtomicPeerCommitAndRetainedStatement(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			transactionAffected(t, ctx, b, "CREATE TABLE u(v INT)", false, 0)
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(1),(2)", prepared, 2)
			transactionAffected(t, ctx, tx, "INSERT INTO u VALUES(9)", prepared, 1)
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(7)", prepared, 1)
			transactionSelect(t, ctx, tx, "SELECT v FROM t ORDER BY v", prepared, 1, 2, 7)
			transactionSelect(t, ctx, b, "SELECT v FROM u ORDER BY v", prepared)
			transactionAffected(t, ctx, tx, "SELECT v FROM t", prepared, 3)
			rows, err := tx.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			nullable, known := types[0].Nullable()
			if nullable || !known {
				rows.Close()
				t.Fatal("transaction lost NOT NULL metadata")
			}
			transactionValues(t, rows, 1, 2, 7)
			// This is a non-Tx borrower statement: retaining public Tx Rows past
			// transaction end would ask database/sql for an unsupported lifetime.
			old, closeOld, err := transactionQuery(ctx, b, "SELECT v FROM t ORDER BY v", prepared)
			if err != nil {
				t.Fatal(err)
			}
			defer closeOld()
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			transactionValues(t, old, 7)
			if err = closeOld(); err != nil {
				t.Fatal(err)
			}
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", prepared, 1, 2, 7)
			transactionSelect(t, ctx, b, "SELECT v FROM u ORDER BY v", prepared, 9)
		})
	}
}

// Compile-time consumer shape witnesses keep direct/prepared Conn/Tx helpers
// on the same public APIs used by native and real-reference behavior tests.
var _ transactionExecutor = (*sql.Conn)(nil)
var _ transactionExecutor = (*sql.Tx)(nil)
