package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func TestSequenceOwnedTableLockSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter], func(t *testing.T) {
				ctx := context.Background()
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
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
				exec := func(c *sql.Conn, q string) {
					t.Helper()
					r, e := c.ExecContext(ctx, q)
					if e != nil {
						t.Fatal(q, e)
					}
					if n, e := r.RowsAffected(); e != nil || n != 0 {
						t.Fatal(n, e)
					}
				}
				diagnostic := func(err error, code, message, position string) {
					t.Helper()
					var got *stuntgres.Error
					want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
					if !errors.As(err, &got) || *got != want {
						t.Fatalf("got %#v (%v), want %#v", got, err, want)
					}
				}
				exec(a, "CREATE TABLE t(c INT)")
				q := "CREATE SEQUENCE s START 23 OWNED BY t.c"
				if alter {
					exec(b, "CREATE SEQUENCE s START 23")
					q = "ALTER SEQUENCE s OWNED BY t.c"
				}
				var stmt *sql.Stmt
				if prepared {
					stmt, err = b.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				run := func(ctx context.Context) (sql.Result, error) {
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
				exec(b, "SET lock_timeout='100ms'")
				r, err := run(ctx)
				if r != nil {
					t.Fatal("failed ownership returned result")
				}
				diagnostic(err, "55P03", "canceling statement due to lock timeout", "")
				exec(b, "RESET lock_timeout")
				bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				r, err = run(bounded)
				cancel()
				var state *stuntgres.Error
				if r != nil || !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &state) {
					t.Fatal("ordinary dependency-lock cancellation", r, err)
				}
				if err = holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				exec(a, "DROP TABLE t")
				var n int64
				if alter {
					if err = b.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&n); err != nil || n != 23 {
						t.Fatal("failed association changed state/dependency", n, err)
					}
				} else {
					err = b.QueryRowContext(ctx, "SELECT last_value FROM s").Scan(&n)
					diagnostic(err, "42P01", `relation "s" does not exist`, "24")
				}
				exec(a, "CREATE TABLE t(c INT)")
				r, err = run(ctx)
				if err != nil {
					t.Fatal("same retained ownership recovery", err)
				}
				if n, e := r.RowsAffected(); e != nil || n != 0 {
					t.Fatal(n, e)
				}
				exec(a, "ALTER TABLE t DROP COLUMN c")
				err = b.QueryRowContext(ctx, "SELECT last_value FROM s").Scan(&n)
				diagnostic(err, "42P01", `relation "s" does not exist`, "24")
				exec(b, "CREATE SEQUENCE healthy START 41")
				if err = b.QueryRowContext(ctx, "SELECT nextval('healthy')").Scan(&n); err != nil || n != 41 {
					t.Fatal("healthy post-drop session", n, err)
				}
			})
		}
	}
}
