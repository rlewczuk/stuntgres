package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestTransactionIsolationSmoke(t *testing.T) {
	for _, name := range []string{"RC", "RU", "RR", "SnapshotPhases", "ReadOnlyDefaultsTiming", "Chain", "AbortedChain", "ConcurrentUpdateDirect", "ConcurrentUpdatePrepared", "ConflictUpdateThenDelete", "DeleteVisibilityAndReadOnly"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			connector, err := stuntgres.NewDriver().OpenConnector("isolation-smoke/" + name)
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			defer a.ExecContext(ctx, "ROLLBACK")
			exec := func(x transactionExecutor, q string, n int64) {
				t.Helper()
				if err := transactionAffected(ctx, x, q, false, n); err != nil {
					t.Fatal(err)
				}
			}
			rows := func(x transactionExecutor, q string, want ...int64) {
				t.Helper()
				if err := transactionSelectValues(ctx, x, q, false, want...); err != nil {
					t.Fatal(err)
				}
			}
			diagnostic := func(err error, code, message string) {
				t.Helper()
				var got *stuntgres.Error
				if !errors.As(err, &got) || got.Code != code || got.Message != message {
					t.Fatalf("got %v want %s %s", err, code, message)
				}
			}
			exec(b, "CREATE TABLE t(v INT)", 0)
			exec(b, "CREATE TABLE u(v INT)", 0)
			exec(b, "CREATE TABLE e(v INT)", 0)
			exec(b, "INSERT INTO t VALUES(1)", 1)
			exec(b, "INSERT INTO u VALUES(10)", 1)
			switch name {
			case "RC", "RU", "RR":
				mode := map[string]string{"RC": "READ COMMITTED", "RU": "READ UNCOMMITTED", "RR": "REPEATABLE READ"}[name]
				exec(a, "BEGIN ISOLATION LEVEL "+mode, 0)
				exec(b, "INSERT INTO t VALUES(2)", 1)
				rows(a, "SELECT v FROM t ORDER BY v", 1, 2)
				exec(b, "BEGIN", 0)
				exec(b, "INSERT INTO t VALUES(3)", 1)
				exec(b, "UPDATE u SET v=20", 1)
				exec(b, "INSERT INTO e VALUES(30)", 1)
				exec(b, "COMMIT", 0)
				if name == "RR" {
					rows(a, "SELECT v FROM t ORDER BY v", 1, 2)
					rows(a, "SELECT v FROM u", 10)
					rows(a, "SELECT v FROM e")
				} else {
					rows(a, "SELECT v FROM t ORDER BY v", 1, 2, 3)
					rows(a, "SELECT v FROM u", 20)
					rows(a, "SELECT v FROM e", 30)
				}
				exec(a, "INSERT INTO t VALUES(4)", 1)
				if name == "RR" {
					rows(a, "SELECT v FROM t ORDER BY v", 1, 2, 4)
				} else {
					rows(a, "SELECT v FROM t ORDER BY v", 1, 2, 3, 4)
				}
				exec(a, "COMMIT", 0)
				rows(b, "SELECT v FROM t ORDER BY v", 1, 2, 3, 4)
			case "SnapshotPhases":
				exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
				held, err := a.PrepareContext(ctx, "SELECT v FROM t")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				exec(b, "UPDATE t SET v=2", 1)
				r, err := held.QueryContext(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var v int64
				if !r.Next() {
					t.Fatal("missing snapshot row")
				}
				if err := r.Scan(&v); err != nil || v != 1 {
					t.Fatal(v, err)
				}
				r.Close()
				_, err = a.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
				diagnostic(err, "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query")
			case "ReadOnlyDefaultsTiming":
				exec(a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY", 0)
				exec(a, "BEGIN", 0)
				var mode string
				if err := a.QueryRowContext(ctx, "SHOW transaction_isolation").Scan(&mode); err != nil || mode != "repeatable read" {
					t.Fatal(mode, err)
				}
				held, err := a.PrepareContext(ctx, "INSERT INTO t VALUES(2)")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				_, err = held.ExecContext(ctx)
				diagnostic(err, "25006", "cannot execute INSERT in a read-only transaction")
				exec(a, "ROLLBACK", 0)
				rows(b, "SELECT v FROM t", 1)
			case "Chain":
				exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", 0)
				rows(a, "SELECT v FROM t", 1)
				exec(b, "INSERT INTO t VALUES(2)", 1)
				exec(a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ COMMITTED, READ WRITE", 0)
				exec(a, "COMMIT AND CHAIN", 0)
				rows(a, "SELECT v FROM t ORDER BY v", 1, 2)
				var mode string
				if err := a.QueryRowContext(ctx, "SHOW transaction_isolation").Scan(&mode); err != nil || mode != "repeatable read" {
					t.Fatal(mode, err)
				}
			case "AbortedChain":
				exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", 0)
				rows(a, "SELECT v FROM t", 1)
				exec(a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY", 0)
				_, err = a.ExecContext(ctx, "DELETE FROM t")
				diagnostic(err, "25006", "cannot execute DELETE in a read-only transaction")
				exec(a, "ROLLBACK AND CHAIN", 0)
				for setting, want := range map[string]string{
					"transaction_isolation": "read committed", "transaction_read_only": "off",
					"default_transaction_isolation": "read committed", "default_transaction_read_only": "off",
				} {
					var value string
					if err := a.QueryRowContext(ctx, "SHOW "+setting).Scan(&value); err != nil || value != want {
						t.Fatal(setting, value, err)
					}
				}
				exec(b, "INSERT INTO t VALUES(2)", 1)
				rows(a, "SELECT v FROM t ORDER BY v", 1, 2)
				exec(a, "INSERT INTO t VALUES(3)", 1)
				exec(a, "ROLLBACK", 0)
				rows(b, "SELECT v FROM t ORDER BY v", 1, 2)
			case "ConflictUpdateThenDelete":
				exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
				rows(a, "SELECT v FROM t", 1)
				exec(b, "UPDATE t SET v=2", 1)
				exec(b, "DELETE FROM t", 1)
				held, e := a.PrepareContext(ctx, "DELETE FROM t")
				if e != nil {
					t.Fatal(e)
				}
				defer held.Close()
				_, err = held.ExecContext(ctx)
				diagnostic(err, "40001", "could not serialize access due to concurrent update")
				exec(a, "ROLLBACK", 0)
				rows(b, "SELECT v FROM t")
			case "ConcurrentUpdateDirect", "ConcurrentUpdatePrepared":
				exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
				rows(a, "SELECT v FROM t", 1)
				exec(b, "UPDATE t SET v=2", 1)
				if name == "ConcurrentUpdatePrepared" {
					held, e := a.PrepareContext(ctx, "UPDATE t SET v=3")
					if e != nil {
						t.Fatal(e)
					}
					defer held.Close()
					_, err = held.ExecContext(ctx)
				} else {
					_, err = a.ExecContext(ctx, "UPDATE t SET v=3")
				}
				diagnostic(err, "40001", "could not serialize access due to concurrent update")
				exec(a, "ROLLBACK", 0)
				rows(b, "SELECT v FROM t", 2)
			case "DeleteVisibilityAndReadOnly":
				exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
				rows(a, "SELECT v FROM t", 1)
				exec(b, "DELETE FROM u", 1)
				rows(a, "SELECT v FROM u", 10)
				exec(a, "SET TRANSACTION READ ONLY", 0)
				_, err = a.ExecContext(ctx, "DELETE FROM t")
				diagnostic(err, "25006", "cannot execute DELETE in a read-only transaction")
				exec(a, "ROLLBACK", 0)
				rows(b, "SELECT v FROM t", 1)
			}
		})
	}
}
