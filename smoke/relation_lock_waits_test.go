package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres"
)

func lockWaitsSmokeConnections(t *testing.T) (*sql.Conn, *sql.Conn) {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(3)
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err = a.ExecContext(context.Background(), "CREATE TABLE t(v INT)"); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func lockWaitsSmokeDiagnostic(t *testing.T, err error, code, message string, position ...string) {
	t.Helper()
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if len(position) != 0 {
		want.Position = position[0]
	}
	if mismatch := transactionDiagnostic(err, want); mismatch != nil {
		t.Fatal(mismatch)
	}
	t.Logf("observed %s: %s", code, message)
}

func TestRelationLockWaitsSmokeNowait(t *testing.T) {
	a, b := lockWaitsSmokeConnections(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.ExecContext(ctx, `CREATE TABLE "T"(v INT)`); err != nil {
		t.Fatal(err)
	}
	holder, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if err = transactionAffected(ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0); err != nil {
		t.Fatal(err)
	}
	if err = transactionAffected(ctx, holder, "INSERT INTO t VALUES(1)", false, 1); err != nil {
		t.Fatal(err)
	}
	if err = transactionAffected(ctx, holder, `LOCK TABLE "T" IN ACCESS EXCLUSIVE MODE`, false, 0); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		for _, name := range []string{"t", "public.t", `"T"`, `public."T"`} {
			tx, err := b.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			query := "LOCK TABLE " + name + " IN ACCESS EXCLUSIVE MODE NOWAIT"
			if prepared {
				stmt, prepareErr := tx.PrepareContext(ctx, query)
				if prepareErr != nil {
					tx.Rollback()
					t.Fatalf("NOWAIT Prepare must be metadata-only: %v", prepareErr)
				}
				_, err = stmt.ExecContext(ctx)
				stmt.Close()
			} else {
				_, err = tx.ExecContext(ctx, query)
			}
			tx.Rollback()
			decoded := name
			if name == `"T"` {
				decoded = "T"
			}
			if name == `public."T"` {
				decoded = "public.T"
			}
			lockWaitsSmokeDiagnostic(t, err, "55P03", `could not obtain lock on relation "`+decoded+`"`)
		}
	}
	if err = holder.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err := b.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for range 2 {
		if err = transactionAffected(ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", true, 0); err != nil {
			t.Fatal("free/self NOWAIT", err)
		}
	}
	if err = transactionSelectValues(ctx, tx, "SELECT v FROM t", false, 1); err != nil {
		t.Fatal(err)
	}
	t.Log("free and retained NOWAIT affected 0; holder commit published row 1")
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	_, err = b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT")
	lockWaitsSmokeDiagnostic(t, err, "25P01", "LOCK TABLE can only be used in transaction blocks")
	for _, prepared := range []bool{false, true} {
		if err = transactionAffected(ctx, b, "INSERT INTO t VALUES(2)", prepared, 1); err != nil {
			t.Fatal(err)
		}
	}
	rolledBack, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rolledBack.Rollback()
	if err = transactionAffected(ctx, rolledBack, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0); err != nil {
		t.Fatal(err)
	}
	if err = transactionAffected(ctx, rolledBack, "INSERT INTO t VALUES(9)", false, 1); err != nil {
		t.Fatal(err)
	}
	if err = rolledBack.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", true, 1, 2, 2); err != nil {
		t.Fatal(err)
	}
	t.Log("outside-block 25P01; direct/cached INSERT affected 1; rollback excluded private 9; final [1 2 2]")
}

func TestRelationLockWaitsSmokeTimeoutCancel(t *testing.T) {
	a, b := lockWaitsSmokeConnections(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var initial string
	if err := b.QueryRowContext(ctx, "SHOW lock_timeout").Scan(&initial); err != nil {
		t.Fatal(err)
	}
	if initial != "0" {
		t.Fatalf("initial lock_timeout = %q, want 0", initial)
	}
	if _, err := b.ExecContext(ctx, "SET lock_timeout = '40ms'"); err != nil {
		t.Fatal(err)
	}
	cached, err := b.PrepareContext(ctx, "SELECT v FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close()
	holder, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if err = transactionAffected(ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0); err != nil {
		t.Fatal(err)
	}
	if err = transactionAffected(ctx, holder, "INSERT INTO t VALUES(1)", false, 1); err != nil {
		t.Fatal(err)
	}
	_, err = b.ExecContext(ctx, "SELECT v FROM t")
	lockWaitsSmokeDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "15")
	rows, err := cached.QueryContext(ctx)
	if rows != nil {
		rows.Close()
	}
	lockWaitsSmokeDiagnostic(t, err, "55P03", "canceling statement due to lock timeout")
	stmt, err := b.PrepareContext(ctx, "SELECT v FROM t")
	if stmt != nil {
		stmt.Close()
	}
	lockWaitsSmokeDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "15")
	tx, err := b.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, "SELECT v FROM t")
	lockWaitsSmokeDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "15")
	_, err = tx.ExecContext(ctx, "SELECT v FROM t")
	tx.Rollback()
	lockWaitsSmokeDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
	if _, err = b.ExecContext(ctx, "SET lock_timeout = 0"); err != nil {
		t.Fatal(err)
	}
	for _, lock := range []string{"1s", "40ms"} {
		if _, err = b.ExecContext(ctx, "SET lock_timeout='"+lock+"'"); err != nil {
			t.Fatal(err)
		}
		if _, err = b.ExecContext(ctx, "SET statement_timeout='40ms'"); err != nil {
			t.Fatal(err)
		}
		_, err = b.ExecContext(ctx, "SELECT v FROM t")
		lockWaitsSmokeDiagnostic(t, err, "57014", "canceling statement due to statement timeout")
		if _, err = b.ExecContext(ctx, "SET statement_timeout=0"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = b.ExecContext(ctx, "SET lock_timeout=0"); err != nil {
		t.Fatal(err)
	}
	bounded, stop := context.WithTimeout(ctx, 40*time.Millisecond)
	_, err = b.ExecContext(bounded, "SELECT v FROM t")
	stop()
	var diagnostic *stuntgres.Error
	if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &diagnostic) {
		t.Fatalf("caller deadline must retain context identity without SQLSTATE: %v", err)
	}
	if err = holder.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = transactionSelectValues(ctx, b, "SELECT v FROM t", true, 1); err != nil {
		t.Fatal(err)
	}
	if err = transactionAffected(ctx, b, "INSERT INTO t VALUES(2)", true, 1); err != nil {
		t.Fatal(err)
	}
	if err = transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 2); err != nil {
		t.Fatal(err)
	}
	t.Log("lock_timeout default 0; direct and semantic Prepare timeout; explicit abort recovered; caller deadline retained; holder unaffected; healthy final rows [1 2]")
}
