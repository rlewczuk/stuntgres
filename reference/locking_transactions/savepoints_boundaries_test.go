package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSavepointsDiagnostics(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ q, word string }{{"SAVEPOINT s", "SAVEPOINT"}, {"RELEASE s", "RELEASE SAVEPOINT"}, {"RELEASE SAVEPOINT s", "RELEASE SAVEPOINT"}, {"ROLLBACK TO s", "ROLLBACK TO SAVEPOINT"}, {"ROLLBACK WORK TO SAVEPOINT s", "ROLLBACK TO SAVEPOINT"}, {"ROLLBACK TRANSACTION TO s", "ROLLBACK TO SAVEPOINT"}} {
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.TransactionError(prepared, tc.q, fixture.ControlsReferenceDiagnostic("25P01", tc.word+" can only be used in transaction blocks")))
			}
		}
		f.Exec(t, f.A, "BEGIN", false, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
		f.Exec(t, f.A, "SAVEPOINT known", false, 0)
		want := fixture.ControlsReferenceDiagnostic("3B001", `savepoint "a"b" does not exist`)
		for _, command := range []string{`RELEASE "a""b"`, `ROLLBACK TO "a""b"`} {
			_, err := f.A.ExecContext(f.Pair.Ctx, command)
			fixture.AssertDiagnostic(t, err, &want)
		}
		for _, command := range []string{"SAVEPOINT other", "RELEASE known"} {
			_, err := f.A.ExecContext(f.Pair.Ctx, command)
			want = fixture.TransactionAborted()
			fixture.AssertDiagnostic(t, err, &want)
		}
		f.Exec(t, f.A, "ROLLBACK TO known", true, 0)
		f.Rows(t, f.A, 1)
		f.NoticesWant(t, "", "")
	})
}

func TestReferenceSavepointsPrepareBoundaries(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		ctx := f.Pair.Ctx
		create, err := f.A.PrepareContext(ctx, "SAVEPOINT s")
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		release, err := f.A.PrepareContext(ctx, "RELEASE s")
		if err != nil {
			t.Fatal(err)
		}
		defer release.Close()
		rollback, err := f.A.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		defer rollback.Close()
		_, err = rollback.ExecContext(ctx)
		want := fixture.ControlsReferenceDiagnostic("25P01", "ROLLBACK TO SAVEPOINT can only be used in transaction blocks")
		fixture.AssertDiagnostic(t, err, &want)
		f.Exec(t, f.A, "BEGIN", false, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
		if _, err = create.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		f.Exec(t, f.A, "INSERT INTO t VALUES(2)", false, 1)
		for _, q := range []string{"RELEASE s", "ROLLBACK TO s", "RELEASE missing"} {
			s, err := f.A.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
		}
		f.Rows(t, f.A, 1, 2)
		_, err = f.A.ExecContext(ctx, "RELEASE missing")
		want = fixture.ControlsReferenceDiagnostic("3B001", `savepoint "missing" does not exist`)
		fixture.AssertDiagnostic(t, err, &want)
		f.Exec(t, f.A, "ROLLBACK TO s", false, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(2)", false, 1)
		unknown, err := f.A.PrepareContext(ctx, "RELEASE missing")
		if err != nil {
			t.Fatal(err)
		}
		r, err := unknown.ExecContext(ctx)
		if closeErr := unknown.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if r != nil {
			t.Fatal("failed held RELEASE returned a result")
		}
		fixture.AssertDiagnostic(t, err, &want)
		want = fixture.TransactionAborted()
		for _, q := range []string{"SAVEPOINT s", "RELEASE s"} {
			_, err = f.A.PrepareContext(ctx, q)
			fixture.AssertDiagnostic(t, err, &want)
		}
		probe, err := f.A.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		probe.Close()
		_, err = create.ExecContext(ctx)
		fixture.AssertDiagnostic(t, err, &want)
		_, err = release.ExecContext(ctx)
		fixture.AssertDiagnostic(t, err, &want)
		if _, err = rollback.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		f.Rows(t, f.A, 1)
		f.Exec(t, f.A, "COMMIT", false, 0)
		f.Exec(t, f.A, "BEGIN", false, 0)
		if _, err = create.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		f.Exec(t, f.A, "INSERT INTO t VALUES(3)", false, 1)
		if _, err = rollback.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = release.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		f.Exec(t, f.A, "COMMIT", false, 0)
		f.Rows(t, f.B, 1)
		f.NoticesWant(t, "", "")
	})
}

func TestReferenceSavepointsIsolation(t *testing.T) {
	for _, mode := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		t.Run(mode, func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
				f.Exec(t, f.B, "INSERT INTO t VALUES(1)", false, 1)
				f.Exec(t, f.A, "BEGIN ISOLATION LEVEL "+mode, false, 0)
				f.Exec(t, f.A, "SAVEPOINT s", false, 0)
				f.Rows(t, f.A, 1)
				f.Exec(t, f.B, "INSERT INTO t VALUES(9)", false, 1)
				f.Exec(t, f.A, "ROLLBACK TO s", false, 0)
				want := []int64{1}
				if mode == "READ COMMITTED" {
					want = append(want, 9)
				}
				f.Rows(t, f.A, want...)
			})
		})
	}
	for _, command := range []string{"SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", "SET transaction_isolation='repeatable read'", "SELECT set_config('transaction_isolation','repeatable read',false)"} {
		t.Run(command, func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
				f.Exec(t, f.A, "BEGIN", false, 0)
				f.Exec(t, f.A, "SAVEPOINT s", false, 0)
				_, err := f.A.ExecContext(f.Pair.Ctx, command)
				message := "SET TRANSACTION ISOLATION LEVEL must not be called in a subtransaction"
				if command == "SELECT set_config('transaction_isolation','repeatable read',false)" {
					message = "SET TRANSACTION ISOLATION LEVEL must be called before any query"
				}
				want := fixture.ControlsReferenceDiagnostic("25001", message)
				fixture.AssertDiagnostic(t, err, &want)
				f.Exec(t, f.A, "ROLLBACK TO s", false, 0)
				f.Exec(t, f.A, "SET TRANSACTION READ ONLY", false, 0)
				_, err = f.A.ExecContext(f.Pair.Ctx, "SET TRANSACTION READ WRITE")
				want = fixture.ControlsReferenceDiagnostic("25001", "cannot set transaction read-write mode inside a read-only transaction")
				fixture.AssertDiagnostic(t, err, &want)
				f.Exec(t, f.A, "ROLLBACK TO s", false, 0)
				f.Exec(t, f.A, "SET TRANSACTION READ ONLY", false, 0)
				f.Exec(t, f.A, "RELEASE s", false, 0)
				var got string
				if err := f.A.QueryRowContext(f.Pair.Ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != "off" {
					t.Fatal(got, err)
				}
			})
		})
	}
}

func TestReferenceSavepointsSnapshotTimingAndNestedModes(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		exec := func(q string) { f.Exec(t, f.A, q, false, 0) }
		show := func(want string) {
			var got string
			if err := f.A.QueryRowContext(f.Pair.Ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != want {
				t.Fatal(got, want, err)
			}
		}
		exec("BEGIN")
		exec("SAVEPOINT parent_mark")
		exec("SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
		exec("SET TRANSACTION READ ONLY")
		exec("SAVEPOINT child")
		exec("RELEASE child")
		show("on")
		exec("RELEASE parent_mark")
		show("off")
		exec("SAVEPOINT s")
		f.Rows(t, f.A)
		exec("ROLLBACK TO s")
		exec("RELEASE s")
		_, err := f.A.ExecContext(f.Pair.Ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
		want := fixture.ControlsReferenceDiagnostic("25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query")
		fixture.AssertDiagnostic(t, err, &want)
		exec("ROLLBACK")
	})
}
