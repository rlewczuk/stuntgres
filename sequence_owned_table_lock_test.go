package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

// A timeout is a positive lock-conflict outcome, not an elapsed-time assertion.
// Manager-specific admission and the three-session closure schedule are covered
// internally; this fixture exercises only consumer-visible database/sql APIs.
func TestSequenceOwnedTableLock(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, timeout := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+map[bool]string{false: "/Context", true: "/LockTimeout"}[timeout], func(t *testing.T) {
					ctx := context.Background()
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					a, b := sequenceConn(t, db), sequenceConn(t, db)
					sequenceExec(t, a, "CREATE TABLE t(c INT)")
					q := "CREATE SEQUENCE s START 17 OWNED BY t.c"
					if alter {
						sequenceExec(t, b, "CREATE SEQUENCE s START 17")
						q = "ALTER SEQUENCE s OWNED BY t.c"
					}
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = b.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					execute := func(ctx context.Context) (sql.Result, error) {
						if stmt != nil {
							return stmt.ExecContext(ctx)
						}
						return b.ExecContext(ctx, q)
					}
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					if _, err = holder.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE"); err != nil {
						t.Fatal(err)
					}
					waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
					if timeout {
						sequenceExec(t, b, "SET lock_timeout='100ms'")
						waitCtx = ctx
					}
					r, err := execute(waitCtx)
					cancel()
					if r != nil {
						t.Fatal("failed ownership returned result", r)
					}
					if timeout {
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "55P03", Message: "canceling statement due to lock timeout"})
						sequenceExec(t, b, "RESET lock_timeout")
					} else {
						var diagnostic *stuntgres.Error
						if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &diagnostic) {
							t.Fatalf("ordinary context failure: %v", err)
						}
					}
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					// Dropping the table proves a failed ALTER published no AUTO
					// dependency. A failed CREATE must not have published its name.
					sequenceExec(t, a, "DROP TABLE t")
					if alter {
						sequenceValue(t, b, "SELECT nextval('s')", 17)
					} else {
						var n int64
						err = b.QueryRowContext(ctx, "SELECT last_value FROM s").Scan(&n)
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "24"})
					}
					// A retained handle must revalidate the current column after
					// the dependency-table lock is released, without publication.
					sequenceExec(t, a, "CREATE TABLE t(other INT)")
					r, err = execute(ctx)
					if r != nil {
						t.Fatal("missing-column failure returned result")
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42703", Message: `column "c" of relation "t" does not exist`})
					sequenceExec(t, a, "DROP TABLE t")
					sequenceExec(t, a, "CREATE TABLE t(c INT)")
					r, err = execute(ctx)
					if err != nil {
						t.Fatal("healthy retained execution", err)
					}
					if n, e := r.RowsAffected(); e != nil || n != 0 {
						t.Fatal(n, e)
					}
					sequenceExec(t, a, "ALTER TABLE t DROP COLUMN c")
					var n int64
					err = b.QueryRowContext(ctx, "SELECT last_value FROM s").Scan(&n)
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "24"})
				})
			}
		}
	}
}

func TestSequenceOwnedTableLockRetention(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+map[bool]string{false: "/Rollback", true: "/Commit"}[commit], func(t *testing.T) {
					ctx := context.Background()
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					a, b := sequenceConn(t, db), sequenceConn(t, db)
					sequenceExec(t, a, "CREATE TABLE t(c INT)")
					q := "CREATE SEQUENCE s OWNED BY t.c"
					if alter {
						sequenceExec(t, a, "CREATE SEQUENCE s")
						q = "ALTER SEQUENCE s OWNED BY t.c"
					}
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					transactionAffected(t, ctx, tx, q, prepared, 0)
					peer, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Rollback()
					bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
					_, err = peer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("ownership statement did not retain table AS", err)
					}
					if err = peer.Rollback(); err != nil {
						t.Fatal(err)
					}
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					peer, err = b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Rollback()
					if _, err = peer.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT"); err != nil {
						t.Fatal("transaction end did not release AS", err)
					}
					if err = peer.Rollback(); err != nil {
						t.Fatal(err)
					}
					sequenceExec(t, b, "DROP TABLE t")
					if alter && !commit {
						sequenceValue(t, b, "SELECT nextval('s')", 1)
					} else {
						var n int64
						err = b.QueryRowContext(ctx, "SELECT last_value FROM s").Scan(&n)
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "24"})
					}
				})
			}
		}
	}
}
