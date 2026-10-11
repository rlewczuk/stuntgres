package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Each selected backend is checked against literal expectations.
func savepointsReference(t *testing.T, scenario func(*testing.T, fixture.ControlsReferenceFixture)) {
	t.Helper()
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, pair, backend.PG)
			f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			defer func() { f.A.ExecContext(pair.Ctx, "ROLLBACK"); f.Exec(t, f.B, "DROP TABLE t", false, 0) }()
			scenario(t, f)
		})
	}
}

func TestReferenceSavepointsAcceptance(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
				f.Exec(t, f.A, "BEGIN", false, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(1)", prepared, 1)
				f.Exec(t, f.A, "SAVEPOINT s", prepared, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(2)", prepared, 1)
				f.Exec(t, f.A, "ROLLBACK TO SAVEPOINT s", prepared, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(3)", prepared, 1)
				f.Rows(t, f.A, 1, 3)
				f.Rows(t, f.B)
				f.Exec(t, f.A, "COMMIT", false, 0)
				f.Rows(t, f.B, 1, 3)
				f.NoticesWant(t, "", "")
			})
		})
	}
}

func TestReferenceSavepointsStack(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		exec := func(q string, count int64) { f.Exec(t, f.A, q, false, count) }
		exec("BEGIN", 0)
		exec("INSERT INTO t VALUES(1)", 1)
		exec("SAVEPOINT s", 0)
		exec("INSERT INTO t VALUES(2)", 1)
		exec("SAVEPOINT s", 0)
		exec("INSERT INTO t VALUES(3)", 1)
		exec("ROLLBACK WORK TO s", 0)
		f.Rows(t, f.A, 1, 2)
		exec("INSERT INTO t VALUES(4)", 1)
		exec("ROLLBACK TRANSACTION TO s", 0)
		f.Rows(t, f.A, 1, 2)
		exec("RELEASE SAVEPOINT s", 0)
		exec("ROLLBACK TO s", 0)
		f.Rows(t, f.A, 1)
		exec("SAVEPOINT child", 0)
		exec("INSERT INTO t VALUES(5)", 1)
		exec("RELEASE s", 0)
		f.Rows(t, f.A, 1, 5)
		f.Rows(t, f.B)
		_, err := f.A.ExecContext(f.Pair.Ctx, "ROLLBACK TO child")
		want := fixture.ControlsReferenceDiagnostic("3B001", `savepoint "child" does not exist`)
		fixture.AssertDiagnostic(t, err, &want)
		exec("ROLLBACK", 0)
		f.Rows(t, f.B)
		f.NoticesWant(t, "", "")
	})
}

func TestReferenceSavepointsErrorRecovery(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		ctx := f.Pair.Ctx
		f.Exec(t, f.A, "BEGIN", false, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
		f.Exec(t, f.A, "SAVEPOINT s", false, 0)
		rollback, err := f.A.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		defer rollback.Close()
		ordinary, err := f.A.PrepareContext(ctx, "SELECT v FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer ordinary.Close()
		err = fixture.TransactionConstraintExec(t, ctx, f.A, "INSERT INTO t VALUES(2),(NULL)", true)
		want := fixture.TransactionNull()
		fixture.AssertDiagnostic(t, err, &want)
		_, err = f.A.ExecContext(ctx, "SELECT v FROM t")
		want = fixture.TransactionAborted()
		fixture.AssertDiagnostic(t, err, &want)
		_, err = ordinary.QueryContext(ctx)
		fixture.AssertDiagnostic(t, err, &want)
		_, err = f.A.PrepareContext(ctx, "SELECT v FROM t")
		fixture.AssertDiagnostic(t, err, &want)
		probe, err := f.A.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		probe.Close()
		_, err = f.A.ExecContext(ctx, "SELECT v FROM t")
		fixture.AssertDiagnostic(t, err, &want)
		_, err = f.A.ExecContext(ctx, "ROLLBACK TO missing")
		want = fixture.ControlsReferenceDiagnostic("3B001", `savepoint "missing" does not exist`)
		fixture.AssertDiagnostic(t, err, &want)
		r, err := rollback.ExecContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, r, 0)
		f.Rows(t, f.A, 1)
		f.Exec(t, f.A, "INSERT INTO t VALUES(3)", false, 1)
		f.Exec(t, f.A, "COMMIT", false, 0)
		f.Rows(t, f.B, 1, 3)
		f.NoticesWant(t, "", "")
	})
}

func TestReferenceSavepointsWriteState(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		f.Exec(t, f.B, "INSERT INTO t VALUES(1),(2)", false, 2)
		f.Exec(t, f.A, "BEGIN", false, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(3)", false, 1)
		f.Exec(t, f.A, "SAVEPOINT s", false, 0)
		f.Exec(t, f.A, "UPDATE t SET v=10 WHERE v=1", false, 1)
		f.Exec(t, f.A, "UPDATE t SET v=30 WHERE v=3", false, 1)
		f.Exec(t, f.A, "DELETE FROM t WHERE v=2", false, 1)
		f.Exec(t, f.A, "INSERT INTO t VALUES(4)", false, 1)
		f.Exec(t, f.A, "ROLLBACK TO s", false, 0)
		f.Rows(t, f.A, 1, 2, 3)
		f.Exec(t, f.A, "COMMIT", false, 0)
		f.Rows(t, f.B, 1, 2, 3)
	})
}

func TestReferenceSavepointsSettings(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		exec := func(q string) { f.Exec(t, f.A, q, false, 0) }
		show := func(want string) {
			var got string
			if err := f.A.QueryRowContext(f.Pair.Ctx, "SHOW application_name").Scan(&got); err != nil || got != want {
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
	})
}

func TestReferenceSavepointsSequences(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		exec := func(q string) { f.Exec(t, f.A, q, false, 0) }
		value := func(q string, want int64) {
			var got int64
			if err := f.A.QueryRowContext(f.Pair.Ctx, q).Scan(&got); err != nil || got != want {
				t.Fatalf("%s = %d want %d: %v", q, got, want, err)
			}
		}
		exec("CREATE SEQUENCE seq CACHE 1")
		defer func() { f.A.ExecContext(f.Pair.Ctx, "ROLLBACK"); f.Exec(t, f.B, "DROP SEQUENCE seq", false, 0) }()
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
	})
}
