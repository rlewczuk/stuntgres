package smoke_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestSavepointsSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("savepoints-smoke")
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
	rows := func(x transactionExecutor, want ...int64) {
		t.Helper()
		if err := transactionSelectValues(ctx, x, "SELECT v FROM t ORDER BY v", false, want...); err != nil {
			t.Fatal(err)
		}
	}
	diagnostic := func(err error, code, message string) {
		t.Helper()
		if err := transactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	exec(b, "CREATE TABLE t(v INT NOT NULL)", 0)
	exec(a, "BEGIN", 0)
	exec(a, "INSERT INTO t VALUES(1)", 1)
	exec(a, "SAVEPOINT s", 0)
	rollback, err := a.PrepareContext(ctx, "ROLLBACK TO s")
	if err != nil {
		t.Fatal(err)
	}
	defer rollback.Close()
	release, err := a.PrepareContext(ctx, "RELEASE s")
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()
	held := func(s *sql.Stmt) {
		t.Helper()
		r, err := s.ExecContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != 0 {
			t.Fatal(n, err)
		}
	}
	exec(a, "INSERT INTO t VALUES(2)", 1)
	held(rollback)
	exec(a, "INSERT INTO t VALUES(3)", 1)
	rows(a, 1, 3)
	rows(b)
	exec(a, "COMMIT", 0)
	rows(b, 1, 3)
	exec(a, "BEGIN", 0)
	exec(a, "SAVEPOINT s", 0)
	exec(a, "INSERT INTO t VALUES(4)", 1)
	exec(a, "SAVEPOINT s", 0)
	exec(a, "INSERT INTO t VALUES(5)", 1)
	held(rollback)
	rows(a, 1, 3, 4)
	held(release)
	held(rollback)
	rows(a, 1, 3)
	exec(a, "ROLLBACK", 0)
	exec(a, "BEGIN", 0)
	exec(a, "INSERT INTO t VALUES(7)", 1)
	exec(a, "SAVEPOINT s", 0)
	bad, err := a.PrepareContext(ctx, "INSERT INTO t VALUES(8),(NULL)")
	if err != nil {
		t.Fatal("constraint must Prepare", err)
	}
	_, err = bad.ExecContext(ctx)
	bad.Close()
	if err := transactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"}); err != nil {
		t.Fatal(err)
	}
	_, err = a.ExecContext(ctx, "SELECT v FROM t")
	diagnostic(err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
	held(rollback)
	rows(a, 1, 3, 7)
	exec(a, "INSERT INTO t VALUES(9)", 1)
	exec(a, "COMMIT", 0)
	rows(b, 1, 3, 7, 9)
	_, err = a.ExecContext(ctx, "SAVEPOINT s")
	diagnostic(err, "25P01", "SAVEPOINT can only be used in transaction blocks")
	exec(a, "BEGIN", 0)
	exec(a, "SAVEPOINT s", 0)
	_, err = a.ExecContext(ctx, "ROLLBACK TO missing")
	diagnostic(err, "3B001", `savepoint "missing" does not exist`)
	held(rollback)
	exec(a, "ROLLBACK", 0)
	exec(a, "BEGIN", 0)
	exec(a, "SAVEPOINT SAVEPOINT", 0)
	exec(a, "INSERT INTO t VALUES(11)", 1)
	exec(a, "ROLLBACK TO SAVEPOINT", 0)
	rows(a, 1, 3, 7, 9)
	exec(a, "INSERT INTO t VALUES(12)", 1)
	exec(a, "RELEASE SAVEPOINT", 0)
	exec(a, "COMMIT", 0)
	rows(b, 1, 3, 7, 9, 12)
}
