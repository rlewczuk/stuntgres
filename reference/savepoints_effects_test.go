//go:build reference

package reference_test

import "testing"

func TestReferenceSavepointsSettingsJournal(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		exec := func(q string) { f.exec(t, f.a, q, false, 0) }
		show := func(q, want string) {
			t.Helper()
			var got string
			if err := f.a.QueryRowContext(f.pair.ctx, q).Scan(&got); err != nil || got != want {
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
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		exec := func(q string) { f.exec(t, f.a, q, false, 0) }
		value := func(q string, want int64) {
			t.Helper()
			var got int64
			if err := f.a.QueryRowContext(f.pair.ctx, q).Scan(&got); err != nil || got != want {
				t.Fatal(q, got, want, err)
			}
		}
		exec("CREATE SEQUENCE seq CACHE 1")
		defer func() {
			f.a.ExecContext(f.pair.ctx, "ROLLBACK")
			for _, name := range []string{"seq", "p", "q", "renamed"} {
				f.b.ExecContext(f.pair.ctx, "DROP SEQUENCE IF EXISTS "+name)
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
		_, err := f.a.ExecContext(f.pair.ctx, "SELECT nextval('q')")
		want := controlsReferenceDiagnostic("42P01", `relation "q" does not exist`)
		want.Position = "16"
		referenceAssertDiagnostic(t, err, &want)
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
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		ctx := f.pair.ctx
		f.exec(t, f.b, "CREATE TABLE u(v INT)", false, 0)
		defer func() {
			f.b.ExecContext(ctx, "ROLLBACK")
			f.a.ExecContext(ctx, "ROLLBACK")
			f.b.ExecContext(ctx, "DROP TABLE u")
		}()
		f.exec(t, f.b, "BEGIN", false, 0)
		f.exec(t, f.b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", false, 0)
		f.exec(t, f.a, "BEGIN", false, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
		f.exec(t, f.a, "SAVEPOINT s", false, 0)
		f.exec(t, f.a, "SET LOCAL statement_timeout='10ms'", false, 0)
		_, err := f.a.ExecContext(ctx, "SELECT v FROM u")
		want := controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
		referenceAssertDiagnostic(t, err, &want)
		_, err = f.a.ExecContext(ctx, "SELECT v FROM t")
		want = referenceTransactionAborted()
		referenceAssertDiagnostic(t, err, &want)
		f.exec(t, f.a, "ROLLBACK TO s", true, 0)
		f.rows(t, f.a, 1)
		f.exec(t, f.a, "INSERT INTO t VALUES(3)", false, 1)
		f.exec(t, f.a, "COMMIT", false, 0)
		f.exec(t, f.b, "ROLLBACK", false, 0)
		f.rows(t, f.b, 1, 3)
	})
}

func TestReferenceSavepointsMixedRelease(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		f.exec(t, f.b, "CREATE TABLE u(v INT)", false, 0)
		defer func() { f.a.ExecContext(f.pair.ctx, "ROLLBACK"); f.b.ExecContext(f.pair.ctx, "DROP TABLE u") }()
		f.exec(t, f.b, "INSERT INTO t VALUES(1),(2)", false, 2)
		exec := func(q string, n int64) { f.exec(t, f.a, q, true, n) }
		exec("BEGIN", 0)
		exec("INSERT INTO u VALUES(9)", 1)
		exec("SAVEPOINT parent_mark", 0)
		exec("INSERT INTO u VALUES(10)", 1)
		exec("UPDATE t SET v=11 WHERE v=1", 1)
		exec("DELETE FROM t WHERE v=2", 1)
		exec("INSERT INTO t VALUES(4),(4)", 2)
		exec("ROLLBACK TO parent_mark", 0)
		f.rows(t, f.a, 1, 2)
		isolationReferenceSelect(t, f.pair.ctx, f.a, "SELECT v FROM u ORDER BY v", false, 9)
		exec("SAVEPOINT child", 0)
		exec("UPDATE t SET v=11 WHERE v=1", 1)
		exec("DELETE FROM t WHERE v=2", 1)
		exec("INSERT INTO t VALUES(4),(4)", 2)
		exec("INSERT INTO u VALUES(10)", 1)
		exec("RELEASE parent_mark", 0)
		f.rows(t, f.b, 1, 2)
		exec("COMMIT", 0)
		f.rows(t, f.b, 4, 4, 11)
		isolationReferenceSelect(t, f.pair.ctx, f.b, "SELECT v FROM u ORDER BY v", false, 9, 10)
	})
}
