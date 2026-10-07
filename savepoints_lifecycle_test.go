package stuntgres_test

import (
	"context"
	"errors"
	"testing"
)

func TestSavepointsCancellationAndControls(t *testing.T) {
	_, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "CREATE TABLE u(v INT)", false, 0)
	transactionAffected(t, ctx, b, "BEGIN", false, 0)
	defer b.ExecContext(ctx, "ROLLBACK")
	transactionAffected(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", false, 0)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	transactionAffected(t, ctx, a, "SAVEPOINT s", false, 0)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := a.ExecContext(canceled, "SAVEPOINT canceled")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
	transactionAffected(t, ctx, a, "SET LOCAL statement_timeout='10ms'", false, 0)
	_, err = a.ExecContext(ctx, "SELECT v FROM u")
	transactionDiagnostic(t, err, controlsDiagnostic("57014", "canceling statement due to statement timeout"))
	_, err = a.ExecContext(ctx, "SELECT v FROM t")
	transactionDiagnostic(t, err, controlsAborted())
	transactionAffected(t, ctx, a, "ROLLBACK TO s", true, 0)
	transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", false, 1)
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionAffected(t, ctx, b, "ROLLBACK", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 3)
}

func TestSavepointsLifecycle(t *testing.T) {
	_, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(1)", false, 1)
	transactionAffected(t, ctx, tx, "SAVEPOINT s", false, 0)
	transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(2)", false, 1)
	transactionDiagnostic(t, transactionConstraintExec(t, ctx, tx, "INSERT INTO t VALUES(NULL)", true), controlsNull())
	transactionAffected(t, ctx, tx, "ROLLBACK TO s", false, 0)
	if err := tx.Commit(); err != nil {
		t.Fatal("qualified Go handle lost after recovery", err)
	}
	transactionSelect(t, ctx, b, "SELECT v FROM t", false, 1)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "SAVEPOINT s", false, 0)
	transactionAffected(t, ctx, a, "COMMIT AND CHAIN", false, 0)
	_, err = a.ExecContext(ctx, "ROLLBACK TO s")
	transactionDiagnostic(t, err, controlsDiagnostic("3B001", `savepoint "s" does not exist`))
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
}
