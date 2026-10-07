package stuntgres_test

import (
	"context"
	"testing"
)

func TestSavepointsSettingsJournal(t *testing.T) {
	_, a, _, _ := controlsFixture(t)
	ctx := context.Background()
	exec := func(q string) { transactionAffected(t, ctx, a, q, false, 0) }
	show := func(q, want string) {
		t.Helper()
		var got string
		if err := a.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
			t.Fatal(q, got, want, err)
		}
	}
	exec("SET application_name='base'")
	exec("BEGIN")
	exec("SET SESSION application_name='candidate'")
	exec("SET LOCAL application_name='before'")
	exec("SAVEPOINT s")
	exec("RESET application_name")
	exec("ROLLBACK TO s")
	show("SHOW application_name", "before")
	exec("RESET ALL")
	exec("ROLLBACK TO s")
	show("SHOW application_name", "before")
	show("SELECT set_config('application_name','function',true)", "function")
	exec("ROLLBACK TO s")
	show("SHOW application_name", "before")
	exec("SAVEPOINT child")
	exec("SET LOCAL application_name='child'")
	exec("RELEASE child")
	show("SHOW application_name", "child")
	exec("ROLLBACK TO s")
	exec("COMMIT")
	show("SHOW application_name", "candidate")
	exec("BEGIN")
	exec("SET application_name='before'")
	exec("SAVEPOINT s")
	exec("SET application_name='after'")
	exec("ROLLBACK TO s")
	exec("ROLLBACK")
	show("SHOW application_name", "candidate")
	exec("BEGIN")
	exec("SAVEPOINT s")
	exec("SET edge.placeholder='created'")
	exec("SET statement_timeout='123ms'")
	exec("ROLLBACK TO s")
	show("SHOW edge.placeholder", "")
	show("SHOW statement_timeout", "0")
	exec("COMMIT")
}

func TestSavepointsSequencesOverlay(t *testing.T) {
	_, a, _, _ := controlsFixture(t)
	ctx := context.Background()
	exec := func(q string) { transactionAffected(t, ctx, a, q, false, 0) }
	value := func(q string, want int64) {
		t.Helper()
		var got int64
		if err := a.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
			t.Fatal(q, got, want, err)
		}
	}
	exec("CREATE SEQUENCE seq CACHE 1")
	exec("BEGIN")
	exec("CREATE SEQUENCE p CACHE 1")
	exec("ALTER SEQUENCE seq RESTART WITH 20")
	exec("SAVEPOINT s")
	value("SELECT nextval('p')", 1)
	value("SELECT setval('seq',50,true)", 50)
	exec("CREATE SEQUENCE q CACHE 1")
	exec("ALTER SEQUENCE seq RESTART WITH 100")
	value("SELECT nextval('seq')", 100)
	exec("ROLLBACK TO s")
	value("SELECT nextval('p')", 2)
	value("SELECT currval('seq')", 100)
	value("SELECT nextval('seq')", 51)
	_, err := a.ExecContext(ctx, "SELECT nextval('q')")
	want := controlsDiagnostic("42P01", `relation "q" does not exist`)
	want.Position = "16"
	transactionDiagnostic(t, err, want)
	exec("ROLLBACK TO s")
	exec("ALTER SEQUENCE p RENAME TO renamed")
	exec("COMMENT ON SEQUENCE renamed IS 'child'")
	exec("DROP SEQUENCE seq")
	exec("ROLLBACK TO s")
	value("SELECT nextval('p')", 3)
	value("SELECT nextval('seq')", 52)
	exec("CREATE SEQUENCE q CACHE 1")
	exec("RELEASE s")
	exec("COMMIT")
	value("SELECT nextval('q')", 1)
}

func TestSavepointsNestedRecoveryAndNames(t *testing.T) {
	_, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	exec := func(q string, n int64) { transactionAffected(t, ctx, a, q, true, n) }
	exec("BEGIN", 0)
	exec("INSERT INTO t VALUES(1)", 1)
	exec(`SAVEPOINT "S"`, 0)
	exec("INSERT INTO t VALUES(2)", 1)
	exec("SAVEPOINT s", 0)
	exec("INSERT INTO t VALUES(3)", 1)
	exec(`ROLLBACK TO "S"`, 0)
	_, err := a.ExecContext(ctx, "ROLLBACK TO s")
	transactionDiagnostic(t, err, controlsDiagnostic("3B001", `savepoint "s" does not exist`))
	exec(`ROLLBACK TO "S"`, 0)
	exec("SAVEPOINT child", 0)
	exec("INSERT INTO t VALUES(4)", 1)
	exec("RELEASE child", 0)
	err = transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(NULL)", true)
	transactionDiagnostic(t, err, controlsNull())
	exec(`ROLLBACK TO "S"`, 0)
	transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
	exec("INSERT INTO t VALUES(3)", 1)
	exec("COMMIT", 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 3)
}

func TestSavepointsMixedRelease(t *testing.T) {
	_, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "CREATE TABLE u(v INT)", false, 0)
	transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1),(2)", false, 2)
	exec := func(q string, n int64) { transactionAffected(t, ctx, a, q, true, n) }
	exec("BEGIN", 0)
	exec("INSERT INTO u VALUES(9)", 1)
	exec("SAVEPOINT parent_mark", 0)
	exec("INSERT INTO u VALUES(10)", 1)
	exec("UPDATE t SET v=11 WHERE v=1", 1)
	exec("DELETE FROM t WHERE v=2", 1)
	exec("INSERT INTO t VALUES(4),(4)", 2)
	exec("ROLLBACK TO parent_mark", 0)
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 2)
	transactionSelect(t, ctx, a, "SELECT v FROM u ORDER BY v", false, 9)
	exec("SAVEPOINT child", 0)
	exec("UPDATE t SET v=11 WHERE v=1", 1)
	exec("DELETE FROM t WHERE v=2", 1)
	exec("INSERT INTO t VALUES(4),(4)", 2)
	exec("INSERT INTO u VALUES(10)", 1)
	exec("RELEASE parent_mark", 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 2)
	exec("COMMIT", 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 4, 4, 11)
	transactionSelect(t, ctx, b, "SELECT v FROM u ORDER BY v", false, 9, 10)
}
