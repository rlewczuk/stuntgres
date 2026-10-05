package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func TestRelationLockIsolation(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	shared := openLocal(t, d, t.Name())
	shared.SetMaxOpenConns(3)
	a, err := shared.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	transactionAffected(t, ctx, a, "CREATE TABLE t(v INT)", false, 0)
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	transactionAffected(t, ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
	for _, db := range []*sql.DB{openLocal(t, d, t.Name()+"/other"), openLocal(t, stuntgres.NewDriver(), t.Name())} {
		transactionAffected(t, ctx, db, "CREATE TABLE t(v INT)", false, 0)
		bounded, cancel := context.WithTimeout(ctx, time.Second)
		transactionAffected(t, bounded, db, "INSERT INTO t VALUES(7)", false, 1)
		rows, err := db.QueryContext(bounded, "SELECT v FROM t")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		transactionValues(t, rows, 7)
		cancel()
	}
	b, err := shared.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	_, err = b.ExecContext(bounded, "INSERT INTO t VALUES(2)")
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("same Driver/DSN did not arbitrate", err)
	}
	tx.Rollback()
	transactionAffected(t, ctx, b, "INSERT INTO t VALUES(2)", false, 1)
}
func TestRelationLockRowsCloseAndAbort(t *testing.T) {
	_, a, b := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "CREATE TABLE t(v INT NOT NULL)", false, 0)
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT v FROM t")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	writer, err := b.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	_, err = writer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	cancel()
	writer.Rollback()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Rows.Close unlocked explicit AS", err)
	}
	tx.Rollback()
	for _, prepared := range []bool{false, true} {
		owner, err := a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		transactionAffected(t, ctx, owner, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", prepared, 0)
		transactionAffected(t, ctx, owner, "INSERT INTO t VALUES(1)", false, 1)
		err = transactionConstraintExec(t, ctx, owner, "INSERT INTO t VALUES(NULL)", prepared)
		transactionDiagnostic(t, err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"})
		rows, err = b.QueryContext(ctx, "SELECT v FROM t")
		if err != nil {
			owner.Rollback()
			t.Fatal("abort retained AX", err)
		}
		transactionValues(t, rows)
		_, err = owner.ExecContext(ctx, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE")
		transactionDiagnostic(t, err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		owner.Rollback()
	}
}
