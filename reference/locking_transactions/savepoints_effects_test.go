package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSavepointsSettingsJournal(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		exec := func(q string) { f.Exec(t, f.A, q, false, 0) }
		show := func(q, want string) {
			t.Helper()
			var got string
			if err := f.A.QueryRowContext(f.Pair.Ctx, q).Scan(&got); err != nil || got != want {
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
	})
}

func TestReferenceSavepointsSequencesOverlay(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		exec := func(q string) { f.Exec(t, f.A, q, false, 0) }
		value := func(q string, want int64) {
			t.Helper()
			var got int64
			if err := f.A.QueryRowContext(f.Pair.Ctx, q).Scan(&got); err != nil || got != want {
				t.Fatal(q, got, want, err)
			}
		}
		exec("CREATE SEQUENCE seq CACHE 1")
		defer func() {
			f.A.ExecContext(f.Pair.Ctx, "ROLLBACK")
			for _, name := range []string{"seq", "p", "q", "renamed"} {
				f.B.ExecContext(f.Pair.Ctx, "DROP SEQUENCE IF EXISTS "+name)
			}
		}()
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
		_, err := f.A.ExecContext(f.Pair.Ctx, "SELECT nextval('q')")
		want := fixture.ControlsReferenceDiagnostic("42P01", `relation "q" does not exist`)
		want.Position = "16"
		fixture.AssertDiagnostic(t, err, &want)
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
	})
}

func TestReferenceSavepointsCancellationAndControls(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		ctx := f.Pair.Ctx
		f.Exec(t, f.B, "CREATE TABLE u(v INT)", false, 0)
		defer func() {
			f.B.ExecContext(ctx, "ROLLBACK")
			f.A.ExecContext(ctx, "ROLLBACK")
			f.B.ExecContext(ctx, "DROP TABLE u")
		}()
		if f.Recorder.RawBackend == fixture.RawPostgres {
			f.Exec(t, f.A, "BEGIN", false, 0)
			f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
			referenceTransactionPreCanceledRetirement(t, &f, "SAVEPOINT canceled", "ROLLBACK TO canceled")
		}
		f.Exec(t, f.B, "BEGIN", false, 0)
		f.Exec(t, f.B, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", false, 0)
		f.Exec(t, f.A, "BEGIN", false, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
		f.Exec(t, f.A, "SAVEPOINT s", false, 0)
		if f.Recorder.RawBackend != fixture.RawPostgres {
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			r, err := f.A.ExecContext(canceled, "SAVEPOINT canceled")
			if !errors.Is(err, context.Canceled) || r != nil {
				t.Fatal("pre-canceled SAVEPOINT", r, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("caller cancellation exposes SQLState", err)
			}
		}
		f.Rows(t, f.A, 1)
		f.Exec(t, f.A, "SET LOCAL statement_timeout='10ms'", false, 0)
		_, err := f.A.ExecContext(ctx, "SELECT v FROM u")
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
		fixture.AssertDiagnostic(t, err, &want)
		_, err = f.A.ExecContext(ctx, "SELECT v FROM t")
		want = fixture.TransactionAborted()
		fixture.AssertDiagnostic(t, err, &want)
		f.Exec(t, f.A, "ROLLBACK TO s", true, 0)
		f.Rows(t, f.A, 1)
		f.Exec(t, f.A, "INSERT INTO t VALUES(3)", false, 1)
		f.Exec(t, f.A, "COMMIT", false, 0)
		f.Exec(t, f.B, "ROLLBACK", false, 0)
		f.Rows(t, f.B, 1, 3)
	})
}

func TestReferenceSavepointsMixedRelease(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		f.Exec(t, f.B, "CREATE TABLE u(v INT)", false, 0)
		defer func() { f.A.ExecContext(f.Pair.Ctx, "ROLLBACK"); f.B.ExecContext(f.Pair.Ctx, "DROP TABLE u") }()
		f.Exec(t, f.B, "INSERT INTO t VALUES(1),(2)", false, 2)
		exec := func(q string, n int64) { f.Exec(t, f.A, q, true, n) }
		exec("BEGIN", 0)
		exec("INSERT INTO u VALUES(9)", 1)
		exec("SAVEPOINT parent_mark", 0)
		exec("INSERT INTO u VALUES(10)", 1)
		exec("UPDATE t SET v=11 WHERE v=1", 1)
		exec("DELETE FROM t WHERE v=2", 1)
		exec("INSERT INTO t VALUES(4),(4)", 2)
		exec("ROLLBACK TO parent_mark", 0)
		f.Rows(t, f.A, 1, 2)
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.TransactionRows(false, "SELECT v FROM u ORDER BY v", 9))
		exec("SAVEPOINT child", 0)
		exec("UPDATE t SET v=11 WHERE v=1", 1)
		exec("DELETE FROM t WHERE v=2", 1)
		exec("INSERT INTO t VALUES(4),(4)", 2)
		exec("INSERT INTO u VALUES(10)", 1)
		exec("RELEASE parent_mark", 0)
		f.Rows(t, f.B, 1, 2)
		exec("COMMIT", 0)
		f.Rows(t, f.B, 4, 4, 11)
		fixture.RunStep(t, f.Pair.Ctx, f.B, f.Recorder, fixture.TransactionRows(false, "SELECT v FROM u ORDER BY v", 9, 10))
	})
}
