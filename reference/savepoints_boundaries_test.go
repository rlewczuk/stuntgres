//go:build reference

package reference_test

import "testing"

func TestReferenceSavepointsDiagnostics(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ q, word string }{{"SAVEPOINT s", "SAVEPOINT"}, {"RELEASE s", "RELEASE SAVEPOINT"}, {"RELEASE SAVEPOINT s", "RELEASE SAVEPOINT"}, {"ROLLBACK TO s", "ROLLBACK TO SAVEPOINT"}, {"ROLLBACK WORK TO SAVEPOINT s", "ROLLBACK TO SAVEPOINT"}, {"ROLLBACK TRANSACTION TO s", "ROLLBACK TO SAVEPOINT"}} {
				referenceRunStep(t, f.pair.ctx, f.a, f.recorder, referenceTransactionError(prepared, tc.q, controlsReferenceDiagnostic("25P01", tc.word+" can only be used in transaction blocks")))
			}
		}
		f.exec(t, f.a, "BEGIN", false, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
		f.exec(t, f.a, "SAVEPOINT known", false, 0)
		_, err := f.a.ExecContext(f.pair.ctx, `RELEASE "a""b"`)
		want := controlsReferenceDiagnostic("3B001", `savepoint "a"b" does not exist`)
		referenceAssertDiagnostic(t, err, &want)
		for _, command := range []string{"SAVEPOINT other", "RELEASE known"} {
			_, err = f.a.ExecContext(f.pair.ctx, command)
			want = referenceTransactionAborted()
			referenceAssertDiagnostic(t, err, &want)
		}
		f.exec(t, f.a, "ROLLBACK TO known", true, 0)
		f.rows(t, f.a, 1)
		f.noticesWant(t, "", "")
	})
}

func TestReferenceSavepointsPrepareBoundaries(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		ctx := f.pair.ctx
		create, err := f.a.PrepareContext(ctx, "SAVEPOINT s")
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		release, err := f.a.PrepareContext(ctx, "RELEASE s")
		if err != nil {
			t.Fatal(err)
		}
		defer release.Close()
		rollback, err := f.a.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		defer rollback.Close()
		_, err = rollback.ExecContext(ctx)
		want := controlsReferenceDiagnostic("25P01", "ROLLBACK TO SAVEPOINT can only be used in transaction blocks")
		referenceAssertDiagnostic(t, err, &want)
		f.exec(t, f.a, "BEGIN", false, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
		if _, err = create.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		f.exec(t, f.a, "INSERT INTO t VALUES(2)", false, 1)
		for _, q := range []string{"RELEASE s", "ROLLBACK TO s", "RELEASE missing"} {
			s, err := f.a.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
		}
		f.rows(t, f.a, 1, 2)
		_, err = f.a.ExecContext(ctx, "RELEASE missing")
		want = controlsReferenceDiagnostic("3B001", `savepoint "missing" does not exist`)
		referenceAssertDiagnostic(t, err, &want)
		want = referenceTransactionAborted()
		for _, q := range []string{"SAVEPOINT s", "RELEASE s"} {
			_, err = f.a.PrepareContext(ctx, q)
			referenceAssertDiagnostic(t, err, &want)
		}
		probe, err := f.a.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		probe.Close()
		_, err = create.ExecContext(ctx)
		referenceAssertDiagnostic(t, err, &want)
		_, err = release.ExecContext(ctx)
		referenceAssertDiagnostic(t, err, &want)
		if _, err = rollback.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		f.rows(t, f.a, 1)
		f.exec(t, f.a, "COMMIT", false, 0)
		f.exec(t, f.a, "BEGIN", false, 0)
		if _, err = create.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		f.exec(t, f.a, "INSERT INTO t VALUES(3)", false, 1)
		if _, err = rollback.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = release.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		f.exec(t, f.a, "COMMIT", false, 0)
		f.rows(t, f.b, 1)
		f.noticesWant(t, "", "")
	})
}

func TestReferenceSavepointsIsolation(t *testing.T) {
	for _, mode := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		t.Run(mode, func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
				f.exec(t, f.b, "INSERT INTO t VALUES(1)", false, 1)
				f.exec(t, f.a, "BEGIN ISOLATION LEVEL "+mode, false, 0)
				f.exec(t, f.a, "SAVEPOINT s", false, 0)
				f.rows(t, f.a, 1)
				f.exec(t, f.b, "INSERT INTO t VALUES(9)", false, 1)
				f.exec(t, f.a, "ROLLBACK TO s", false, 0)
				want := []int64{1}
				if mode == "READ COMMITTED" {
					want = append(want, 9)
				}
				f.rows(t, f.a, want...)
			})
		})
	}
	for _, command := range []string{"SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", "SET transaction_isolation='repeatable read'", "SELECT set_config('transaction_isolation','repeatable read',false)"} {
		t.Run(command, func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
				f.exec(t, f.a, "BEGIN", false, 0)
				f.exec(t, f.a, "SAVEPOINT s", false, 0)
				_, err := f.a.ExecContext(f.pair.ctx, command)
				message := "SET TRANSACTION ISOLATION LEVEL must not be called in a subtransaction"
				if command == "SELECT set_config('transaction_isolation','repeatable read',false)" {
					message = "SET TRANSACTION ISOLATION LEVEL must be called before any query"
				}
				want := controlsReferenceDiagnostic("25001", message)
				referenceAssertDiagnostic(t, err, &want)
				f.exec(t, f.a, "ROLLBACK TO s", false, 0)
				f.exec(t, f.a, "SET TRANSACTION READ ONLY", false, 0)
				_, err = f.a.ExecContext(f.pair.ctx, "SET TRANSACTION READ WRITE")
				want = controlsReferenceDiagnostic("25001", "cannot set transaction read-write mode inside a read-only transaction")
				referenceAssertDiagnostic(t, err, &want)
				f.exec(t, f.a, "ROLLBACK TO s", false, 0)
				f.exec(t, f.a, "SET TRANSACTION READ ONLY", false, 0)
				f.exec(t, f.a, "RELEASE s", false, 0)
				var got string
				if err := f.a.QueryRowContext(f.pair.ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != "off" {
					t.Fatal(got, err)
				}
			})
		})
	}
}

func TestReferenceSavepointsSnapshotTimingAndNestedModes(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		exec := func(q string) { f.exec(t, f.a, q, false, 0) }
		show := func(want string) {
			var got string
			if err := f.a.QueryRowContext(f.pair.ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != want {
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
		f.rows(t, f.a)
		exec("ROLLBACK TO s")
		exec("RELEASE s")
		_, err := f.a.ExecContext(f.pair.ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
		want := controlsReferenceDiagnostic("25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query")
		referenceAssertDiagnostic(t, err, &want)
		exec("ROLLBACK")
	})
}
