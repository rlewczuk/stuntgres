package reference_test

import "testing"

// Each selected backend is checked against literal expectations.
func savepointsReference(t *testing.T, scenario func(*testing.T, controlsReferenceFixture)) {
	t.Helper()
	pair := newReferenceScenario(t)
	for _, backend := range pair.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, pair, backend.pg)
			f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			defer func() { f.a.ExecContext(pair.ctx, "ROLLBACK"); f.exec(t, f.b, "DROP TABLE t", false, 0) }()
			scenario(t, f)
		})
	}
}

func TestReferenceSavepointsAcceptance(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
				f.exec(t, f.a, "BEGIN", false, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(1)", prepared, 1)
				f.exec(t, f.a, "SAVEPOINT s", prepared, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(2)", prepared, 1)
				f.exec(t, f.a, "ROLLBACK TO SAVEPOINT s", prepared, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(3)", prepared, 1)
				f.rows(t, f.a, 1, 3)
				f.rows(t, f.b)
				f.exec(t, f.a, "COMMIT", false, 0)
				f.rows(t, f.b, 1, 3)
				f.noticesWant(t, "", "")
			})
		})
	}
}

func TestReferenceSavepointsStack(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		exec := func(q string, count int64) { f.exec(t, f.a, q, false, count) }
		exec("BEGIN", 0)
		exec("INSERT INTO t VALUES(1)", 1)
		exec("SAVEPOINT s", 0)
		exec("INSERT INTO t VALUES(2)", 1)
		exec("SAVEPOINT s", 0)
		exec("INSERT INTO t VALUES(3)", 1)
		exec("ROLLBACK WORK TO s", 0)
		f.rows(t, f.a, 1, 2)
		exec("INSERT INTO t VALUES(4)", 1)
		exec("ROLLBACK TRANSACTION TO s", 0)
		f.rows(t, f.a, 1, 2)
		exec("RELEASE SAVEPOINT s", 0)
		exec("ROLLBACK TO s", 0)
		f.rows(t, f.a, 1)
		exec("SAVEPOINT child", 0)
		exec("INSERT INTO t VALUES(5)", 1)
		exec("RELEASE s", 0)
		f.rows(t, f.a, 1, 5)
		f.rows(t, f.b)
		_, err := f.a.ExecContext(f.pair.ctx, "ROLLBACK TO child")
		want := controlsReferenceDiagnostic("3B001", `savepoint "child" does not exist`)
		referenceAssertDiagnostic(t, err, &want)
		exec("ROLLBACK", 0)
		f.rows(t, f.b)
		f.noticesWant(t, "", "")
	})
}

func TestReferenceSavepointsErrorRecovery(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		ctx := f.pair.ctx
		f.exec(t, f.a, "BEGIN", false, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
		f.exec(t, f.a, "SAVEPOINT s", false, 0)
		rollback, err := f.a.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		defer rollback.Close()
		ordinary, err := f.a.PrepareContext(ctx, "SELECT v FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer ordinary.Close()
		err = transactionConstraintExec(t, ctx, f.a, "INSERT INTO t VALUES(2),(NULL)", true)
		want := referenceTransactionNull()
		referenceAssertDiagnostic(t, err, &want)
		_, err = f.a.ExecContext(ctx, "SELECT v FROM t")
		want = referenceTransactionAborted()
		referenceAssertDiagnostic(t, err, &want)
		_, err = ordinary.QueryContext(ctx)
		referenceAssertDiagnostic(t, err, &want)
		_, err = f.a.PrepareContext(ctx, "SELECT v FROM t")
		referenceAssertDiagnostic(t, err, &want)
		probe, err := f.a.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		probe.Close()
		_, err = f.a.ExecContext(ctx, "SELECT v FROM t")
		referenceAssertDiagnostic(t, err, &want)
		_, err = f.a.ExecContext(ctx, "ROLLBACK TO missing")
		want = controlsReferenceDiagnostic("3B001", `savepoint "missing" does not exist`)
		referenceAssertDiagnostic(t, err, &want)
		r, err := rollback.ExecContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		referenceLockAffected(t, r, 0)
		f.rows(t, f.a, 1)
		f.exec(t, f.a, "INSERT INTO t VALUES(3)", false, 1)
		f.exec(t, f.a, "COMMIT", false, 0)
		f.rows(t, f.b, 1, 3)
		f.noticesWant(t, "", "")
	})
}

func TestReferenceSavepointsWriteState(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		f.exec(t, f.b, "INSERT INTO t VALUES(1),(2)", false, 2)
		f.exec(t, f.a, "BEGIN", false, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(3)", false, 1)
		f.exec(t, f.a, "SAVEPOINT s", false, 0)
		f.exec(t, f.a, "UPDATE t SET v=10 WHERE v=1", false, 1)
		f.exec(t, f.a, "UPDATE t SET v=30 WHERE v=3", false, 1)
		f.exec(t, f.a, "DELETE FROM t WHERE v=2", false, 1)
		f.exec(t, f.a, "INSERT INTO t VALUES(4)", false, 1)
		f.exec(t, f.a, "ROLLBACK TO s", false, 0)
		f.rows(t, f.a, 1, 2, 3)
		f.exec(t, f.a, "COMMIT", false, 0)
		f.rows(t, f.b, 1, 2, 3)
	})
}

func TestReferenceSavepointsSettings(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		exec := func(q string) { f.exec(t, f.a, q, false, 0) }
		show := func(want string) {
			var got string
			if err := f.a.QueryRowContext(f.pair.ctx, "SHOW application_name").Scan(&got); err != nil || got != want {
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
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		exec := func(q string) { f.exec(t, f.a, q, false, 0) }
		value := func(q string, want int64) {
			var got int64
			if err := f.a.QueryRowContext(f.pair.ctx, q).Scan(&got); err != nil || got != want {
				t.Fatalf("%s = %d want %d: %v", q, got, want, err)
			}
		}
		exec("CREATE SEQUENCE seq CACHE 1")
		defer func() { f.a.ExecContext(f.pair.ctx, "ROLLBACK"); f.exec(t, f.b, "DROP SEQUENCE seq", false, 0) }()
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
