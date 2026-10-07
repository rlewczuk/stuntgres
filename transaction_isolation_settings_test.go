package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"
)

func isolationShow(t *testing.T, c *sql.Conn, name, want string) {
	t.Helper()
	var got string
	if err := c.QueryRowContext(context.Background(), "SHOW "+name).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s=%q, want %q", name, got, want)
	}
}
func isolationError(t *testing.T, c *sql.Conn, query, code, message string, prepared bool) {
	t.Helper()
	err := transactionConstraintExec(t, context.Background(), c, query, prepared)
	transactionDiagnostic(t, err, controlsDiagnostic(code, message))
}
func TestTransactionIsolationTiming(t *testing.T) {
	for _, first := range []string{"SELECT num_nonnulls(1)", "SELECT current_user", "SELECT current_setting('application_name')", "SELECT v FROM t", "INSERT INTO t VALUES(2)", "INSERT INTO t SELECT v FROM t", "UPDATE t SET v=3", "DELETE FROM t"} {
		for _, prepare := range []bool{false, true} {
			t.Run(first+map[bool]string{false: "/execute", true: "/prepare"}[prepare], func(t *testing.T) {
				_, a, b := transactionConnections(t)
				ctx := context.Background()
				transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
				transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
				if prepare {
					statement, err := a.PrepareContext(ctx, first)
					if err != nil {
						t.Fatal(err)
					}
					statement.Close()
				} else {
					if _, err := a.ExecContext(ctx, first); err != nil {
						t.Fatal(err)
					}
				}
				transactionAffected(t, ctx, a, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", false, 0)
				transactionAffected(t, ctx, a, "SET TRANSACTION READ ONLY", false, 0)
				isolationError(t, a, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED", "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query", false)
				isolationError(t, a, "SELECT num_nonnulls(1)", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false)
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
			})
		}
	}
	for _, query := range []string{"SET TRANSACTION READ WRITE", "SET transaction_read_only TO off"} {
		_, a, _ := transactionConnections(t)
		ctx := context.Background()
		transactionAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
		transactionAffected(t, ctx, a, "SELECT num_nonnulls(1)", false, 1)
		isolationError(t, a, query, "25001", "transaction read-write mode must be set before any query", false)
		transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	}
	_, a, _ := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	isolationError(t, a, "SELECT set_config('transaction_isolation','repeatable read',false)", "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query", false)
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	for _, modes := range []string{"ISOLATION LEVEL SERIALIZABLE", "DEFERRABLE", "NOT DEFERRABLE"} {
		isolationError(t, a, "BEGIN "+modes, "0A000", "transaction mode is unsupported", false)
	}
}
func TestTransactionIsolationDefaults(t *testing.T) {
	_, a, b := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY", false, 0)
	isolationShow(t, b, "default_transaction_isolation", "read committed")
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	isolationShow(t, a, "transaction_isolation", "repeatable read")
	isolationShow(t, a, "transaction_read_only", "on")
	transactionAffected(t, ctx, a, "SET default_transaction_isolation TO 'read committed'", false, 0)
	transactionAffected(t, ctx, a, "SET default_transaction_read_only TO off", false, 0)
	transactionAffected(t, ctx, a, "COMMIT AND CHAIN", false, 0)
	isolationShow(t, a, "transaction_isolation", "repeatable read")
	isolationShow(t, a, "transaction_read_only", "on")
	transactionAffected(t, ctx, a, "ROLLBACK AND CHAIN", false, 0)
	isolationShow(t, a, "transaction_isolation", "repeatable read")
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	isolationShow(t, a, "transaction_isolation", "read committed")
	isolationShow(t, a, "transaction_read_only", "off")
	transactionAffected(t, ctx, a, "SET LOCAL default_transaction_isolation TO 'repeatable read'", false, 0)
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	isolationShow(t, a, "default_transaction_isolation", "read committed")
	for _, end := range []string{"ROLLBACK", "COMMIT"} {
		transactionAffected(t, ctx, a, "BEGIN", false, 0)
		transactionAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ UNCOMMITTED", false, 0)
		transactionAffected(t, ctx, a, end, false, 0)
		want := "read committed"
		if end == "COMMIT" {
			want = "read uncommitted"
		}
		isolationShow(t, a, "default_transaction_isolation", want)
	}
	transactionAffected(t, ctx, a, "SET default_transaction_isolation TO 'repeatable read'", false, 0)
	for _, level := range []sql.IsolationLevel{sql.LevelDefault, sql.LevelReadCommitted, sql.LevelReadUncommitted, sql.LevelRepeatableRead} {
		tx, err := a.BeginTx(ctx, &sql.TxOptions{Isolation: level, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if err := tx.QueryRowContext(ctx, "SHOW transaction_isolation").Scan(&got); err != nil {
			t.Fatal(err)
		}
		want := map[sql.IsolationLevel]string{sql.LevelDefault: "repeatable read", sql.LevelReadCommitted: "read committed", sql.LevelReadUncommitted: "read uncommitted", sql.LevelRepeatableRead: "repeatable read"}[level]
		if got != want {
			t.Fatalf("got %s want %s", got, want)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestTransactionIsolationResetRules(t *testing.T) {
	for _, name := range []string{"transaction_isolation", "transaction_read_only"} {
		for _, query := range []string{"RESET " + name, "SET " + name + " TO DEFAULT", "SELECT set_config('" + name + "',NULL,false)"} {
			_, a, _ := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			isolationError(t, a, query, "0A000", "parameter \""+name+"\" cannot be reset", false)
			transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
		}
	}
	_, a, _ := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY", false, 0)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "SELECT num_nonnulls(1)", false, 1)
	transactionAffected(t, ctx, a, "RESET ALL", false, 0)
	isolationShow(t, a, "transaction_isolation", "repeatable read")
	isolationShow(t, a, "transaction_read_only", "on")
	isolationShow(t, a, "default_transaction_isolation", "read committed")
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	isolationShow(t, a, "default_transaction_isolation", "repeatable read")
}
func TestTransactionIsolationReadOnly(t *testing.T) {
	tags := []struct{ query, tag string }{
		{"INSERT INTO t VALUES(2)", "INSERT"}, {"UPDATE t SET v=2", "UPDATE"}, {"DELETE FROM t", "DELETE"},
		{"CREATE TABLE forbidden(v INT)", "CREATE TABLE"}, {"DROP TABLE t", "DROP TABLE"},
		{"ALTER TABLE t RENAME TO forbidden", "ALTER TABLE"}, {"ALTER TABLE t RENAME v TO w", "ALTER TABLE"}, {"ALTER TABLE t ALTER v SET NOT NULL", "ALTER TABLE"}, {"ALTER TABLE t DROP v", "ALTER TABLE"},
		{"CREATE SCHEMA forbidden", "CREATE SCHEMA"}, {"ALTER SCHEMA public RENAME TO forbidden", "ALTER SCHEMA"}, {"DROP SCHEMA public", "DROP SCHEMA"},
		{"CREATE ROLE forbidden", "CREATE ROLE"}, {"DROP ROLE postgres", "DROP ROLE"},
		{"COMMENT ON TABLE t IS 'x'", "COMMENT"}, {"COMMENT ON COLUMN t.v IS 'x'", "COMMENT"}, {"COMMENT ON SCHEMA public IS 'x'", "COMMENT"}, {"COMMENT ON ROLE postgres IS 'x'", "COMMENT"},
	}
	for _, item := range tags {
		for _, prepared := range []bool{false, true} {
			t.Run(item.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, b := transactionConnections(t)
				ctx := context.Background()
				transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
				var held *sql.Stmt
				if prepared && item.tag != "INSERT" && item.tag != "UPDATE" && item.tag != "DELETE" {
					var err error
					held, err = a.PrepareContext(ctx, item.query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				transactionAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
				if held != nil {
					_, err := held.ExecContext(ctx)
					transactionDiagnostic(t, err, controlsDiagnostic("25006", "cannot execute "+item.tag+" in a read-only transaction"))
				} else {
					isolationError(t, a, item.query, "25006", "cannot execute "+item.tag+" in a read-only transaction", prepared)
				}
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
				transactionSelect(t, ctx, b, "SELECT v FROM t", false, 1)
			})
		}
	}
	_, a, b := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
	statement, err := a.PrepareContext(ctx, "INSERT INTO t VALUES(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	transactionAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
	_, err = statement.ExecContext(ctx)
	transactionDiagnostic(t, err, controlsDiagnostic("25006", "cannot execute INSERT in a read-only transaction"))
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", false, 1)
	transactionAffected(t, ctx, a, "SET TRANSACTION READ ONLY", false, 0)
	isolationError(t, a, "INSERT INTO t VALUES(4)", "25006", "cannot execute INSERT in a read-only transaction", false)
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t", false)
}
