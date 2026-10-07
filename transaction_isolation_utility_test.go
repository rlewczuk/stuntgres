package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestTransactionIsolationReadOnlyUtilityPrecedence(t *testing.T) {
	for _, utility := range []struct{ query, tag string }{
		{"ALTER TABLE target RENAME TO renamed", "ALTER TABLE"},
		{"COMMENT ON TABLE target IS 'forbidden'", "COMMENT"},
	} {
		for _, missing := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(utility.tag+map[bool]string{false: "/locked", true: "/missing"}[missing]+map[bool]string{false: "/direct", true: "/held_prepared"}[prepared], func(t *testing.T) {
					db, a, b := transactionConnections(t)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
					transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
					transactionAffected(t, ctx, b, "COMMENT ON TABLE t IS 'original'", false, 0)
					var before bytes.Buffer
					if err := stuntgres.Dump(ctx, db, &before); err != nil {
						t.Fatal(err)
					}
					name := "t"
					if missing {
						name = "missing"
					}
					query := strings.Replace(utility.query, "target", name, 1)
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = a.PrepareContext(ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					if !missing {
						transactionAffected(t, ctx, b, "BEGIN", false, 0)
						defer b.ExecContext(context.Background(), "ROLLBACK")
						transactionAffected(t, ctx, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
					}
					transactionAffected(t, ctx, a, "SET lock_timeout TO '20ms'", false, 0)
					transactionAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
					defer a.ExecContext(context.Background(), "ROLLBACK")
					var err error
					if prepared {
						_, err = stmt.ExecContext(ctx)
					} else {
						_, err = a.ExecContext(ctx, query)
					}
					transactionDiagnostic(t, err, controlsDiagnostic("25006", "cannot execute "+utility.tag+" in a read-only transaction"))
					isolationError(t, a, "SELECT current_user", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false)
					transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
					if !missing {
						transactionAffected(t, ctx, b, "ROLLBACK", false, 0)
					}
					transactionSelect(t, ctx, b, "SELECT v FROM t", false, 1)
					transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
					// Dump observes both catalog names and comment text, not just rows.
					var after bytes.Buffer
					if err := stuntgres.Dump(ctx, db, &after); err != nil {
						t.Fatal(err)
					}
					if after.String() != before.String() {
						t.Fatalf("forbidden utility changed persistent state:\nbefore:\n%safter:\n%s", before.String(), after.String())
					}
				})
			}
		}
	}
}

// Read-only execution precedence does not authorize transactional utilities or
// make explicit-transaction utility Prepare claim executable support.
func TestTransactionIsolationUtilityUnsupportedBoundary(t *testing.T) {
	for _, query := range []string{"ALTER TABLE t RENAME TO renamed", "COMMENT ON TABLE t IS 'forbidden'"} {
		t.Run(query, func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
			want := controlsDiagnostic("0A000", "statement is unsupported in an explicit transaction")
			for _, mode := range []string{"READ ONLY", "READ WRITE"} {
				transactionAffected(t, ctx, a, "BEGIN "+mode, false, 0)
				stmt, err := a.PrepareContext(ctx, query)
				if stmt != nil {
					stmt.Close()
				}
				transactionDiagnostic(t, err, want)
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
			}
			held, err := a.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, prepared := range []bool{false, true} {
				transactionAffected(t, ctx, a, "BEGIN READ WRITE", false, 0)
				if prepared {
					_, err = held.ExecContext(ctx)
				} else {
					_, err = a.ExecContext(ctx, query)
				}
				transactionDiagnostic(t, err, want)
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
				transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
			}
		})
	}
}
