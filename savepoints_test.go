package stuntgres_test

import (
	"context"
	"testing"
)

func TestSavepointsAcceptance(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b, notices := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", prepared, 1)
			transactionAffected(t, ctx, a, "SAVEPOINT s", prepared, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(2)", prepared, 1)
			transactionAffected(t, ctx, a, "ROLLBACK TO SAVEPOINT s", prepared, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", prepared, 1)
			transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", prepared, 1, 3)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false)
			transactionAffected(t, ctx, a, "COMMIT", false, 0)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 3)
			controlsNotices(t, notices, "", "")
		})
	}
}

func TestSavepointsStack(t *testing.T) {
	_, a, b, notices := controlsFixture(t)
	ctx := context.Background()
	exec := func(q string, count int64) { transactionAffected(t, ctx, a, q, false, count) }
	exec("BEGIN", 0)
	exec("INSERT INTO t VALUES(1)", 1)
	exec("SAVEPOINT s", 0)
	exec("INSERT INTO t VALUES(2)", 1)
	exec("SAVEPOINT s", 0)
	exec("INSERT INTO t VALUES(3)", 1)
	exec("ROLLBACK WORK TO s", 0)
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 2)
	exec("INSERT INTO t VALUES(4)", 1)
	exec("ROLLBACK TRANSACTION TO s", 0)
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 2)
	exec("RELEASE SAVEPOINT s", 0)
	exec("ROLLBACK TO s", 0)
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1)
	exec("SAVEPOINT child", 0)
	exec("INSERT INTO t VALUES(5)", 1)
	exec("RELEASE s", 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false)
	_, err := a.ExecContext(ctx, "ROLLBACK TO child")
	transactionDiagnostic(t, err, controlsDiagnostic("3B001", `savepoint "child" does not exist`))
	exec("ROLLBACK", 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false)
	controlsNotices(t, notices, "", "")
}

func TestSavepointsErrorRecovery(t *testing.T) {
	_, a, b, notices := controlsFixture(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	transactionAffected(t, ctx, a, "SAVEPOINT s", false, 0)
	rollback, err := a.PrepareContext(ctx, "ROLLBACK TO s")
	if err != nil {
		t.Fatal(err)
	}
	defer rollback.Close()
	ordinary, err := a.PrepareContext(ctx, "SELECT v FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Close()
	err = transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(2),(NULL)", true)
	transactionDiagnostic(t, err, controlsNull())
	_, err = a.ExecContext(ctx, "SELECT v FROM t")
	transactionDiagnostic(t, err, controlsAborted())
	_, err = ordinary.QueryContext(ctx)
	transactionDiagnostic(t, err, controlsAborted())
	_, err = a.PrepareContext(ctx, "SELECT v FROM t")
	transactionDiagnostic(t, err, controlsAborted())
	probe, err := a.PrepareContext(ctx, "ROLLBACK TO s")
	if err != nil {
		t.Fatal(err)
	}
	probe.Close()
	_, err = a.ExecContext(ctx, "SELECT v FROM t")
	transactionDiagnostic(t, err, controlsAborted())
	_, err = a.ExecContext(ctx, "ROLLBACK TO missing")
	transactionDiagnostic(t, err, controlsDiagnostic("3B001", `savepoint "missing" does not exist`))
	r, err := rollback.ExecContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", false, 1)
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 3)
	controlsNotices(t, notices, "", "")
}

func TestSavepointsWriteState(t *testing.T) {
	_, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1),(2)", false, 2)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", false, 1)
	transactionAffected(t, ctx, a, "SAVEPOINT s", false, 0)
	transactionAffected(t, ctx, a, "UPDATE t SET v=10 WHERE v=1", false, 1)
	transactionAffected(t, ctx, a, "UPDATE t SET v=30 WHERE v=3", false, 1)
	transactionAffected(t, ctx, a, "DELETE FROM t WHERE v=2", false, 1)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(4)", false, 1)
	transactionAffected(t, ctx, a, "ROLLBACK TO s", false, 0)
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
}

func TestSavepointsSettings(t *testing.T) {
	_, a, _, _ := controlsFixture(t)
	ctx := context.Background()
	exec := func(q string) { transactionAffected(t, ctx, a, q, false, 0) }
	show := func(want string) {
		var got string
		if err := a.QueryRowContext(ctx, "SHOW application_name").Scan(&got); err != nil || got != want {
			t.Fatalf("application_name %q want %q: %v", got, want, err)
		}
	}
	exec("SET application_name='base'")
	exec("BEGIN")
	exec("SET SESSION application_name='before'")
	exec("SAVEPOINT s")
	exec("SET SESSION application_name='after'")
	exec("SET LOCAL application_name='local'")
	exec("ROLLBACK TO s")
	show("before")
	exec("COMMIT")
	show("before")
}

func TestSavepointsSequences(t *testing.T) {
	_, a, _, _ := controlsFixture(t)
	ctx := context.Background()
	exec := func(q string) { transactionAffected(t, ctx, a, q, false, 0) }
	value := func(q string, want int64) {
		var got int64
		if err := a.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
			t.Fatalf("%s = %d want %d: %v", q, got, want, err)
		}
	}
	exec("CREATE SEQUENCE seq CACHE 1")
	value("SELECT nextval('seq')", 1)
	exec("BEGIN")
	exec("SAVEPOINT s")
	value("SELECT nextval('seq')", 2)
	exec("ROLLBACK TO s")
	value("SELECT currval('seq')", 2)
	value("SELECT nextval('seq')", 3)
	exec("ALTER SEQUENCE seq RESTART WITH 100")
	value("SELECT nextval('seq')", 100)
	exec("ROLLBACK TO s")
	value("SELECT currval('seq')", 100)
	value("SELECT nextval('seq')", 4)
	exec("COMMIT")
}
