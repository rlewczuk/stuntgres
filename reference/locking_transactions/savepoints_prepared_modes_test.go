package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSavepointsPreparedCharacteristics(t *testing.T) {
	for _, tc := range []struct {
		name, query, message string
		value                bool
	}{
		{"transaction_isolation_changed", "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", "SET TRANSACTION ISOLATION LEVEL must not be called in a subtransaction", false},
		{"setting_isolation_changed", "SET transaction_isolation='repeatable read'", "SET TRANSACTION ISOLATION LEVEL must not be called in a subtransaction", false},
		{"query_isolation_changed", "SELECT set_config('transaction_isolation','repeatable read',false)", "SET TRANSACTION ISOLATION LEVEL must be called before any query", true},
		{"transaction_isolation_same", "SET TRANSACTION ISOLATION LEVEL READ COMMITTED", "", false},
		{"setting_isolation_same", "SET transaction_isolation='read committed'", "", false},
		{"query_isolation_same", "SELECT set_config('transaction_isolation','read committed',false)", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
				f.Exec(t, f.A, "BEGIN ISOLATION LEVEL READ COMMITTED", false, 0)
				f.Exec(t, f.A, "SAVEPOINT known", false, 0)
				stmt, err := f.A.PrepareContext(f.Pair.Ctx, tc.query)
				if err != nil {
					t.Fatal("assignment must Prepare without applying modes", err)
				}
				defer stmt.Close()
				var got string
				if err := f.A.QueryRowContext(f.Pair.Ctx, "SHOW transaction_isolation").Scan(&got); err != nil || got != "read committed" {
					t.Fatal("Prepare changed isolation", got, err)
				}
				if tc.message != "" {
					_, err = stmt.ExecContext(f.Pair.Ctx)
					want := fixture.ControlsReferenceDiagnostic("25001", tc.message)
					fixture.AssertDiagnostic(t, err, &want)
					_, err = f.A.ExecContext(f.Pair.Ctx, "SELECT v FROM t")
					want = fixture.TransactionAborted()
					fixture.AssertDiagnostic(t, err, &want)
					f.Exec(t, f.A, "ROLLBACK TO known", true, 0)
				} else if tc.value {
					if err := stmt.QueryRowContext(f.Pair.Ctx).Scan(&got); err != nil || got != "read committed" {
						t.Fatal("idempotent prepared query", got, err)
					}
				} else {
					result, err := stmt.ExecContext(f.Pair.Ctx)
					if err != nil {
						t.Fatal("idempotent prepared assignment", err)
					}
					fixture.LockAffected(t, result, 0)
				}
				if err := f.A.QueryRowContext(f.Pair.Ctx, "SHOW transaction_isolation").Scan(&got); err != nil || got != "read committed" {
					t.Fatal(got, err)
				}
				f.Exec(t, f.A, "ROLLBACK", false, 0)
				f.NoticesWant(t, "", "")
			})
		})
	}
	for _, query := range []string{"SET TRANSACTION READ WRITE", "SET transaction_read_only=off", "SELECT set_config('transaction_read_only','off',false)"} {
		t.Run("read_write_precedence/"+query, func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
				f.Exec(t, f.A, "BEGIN READ WRITE", false, 0)
				f.Exec(t, f.A, "SAVEPOINT known", false, 0)
				f.Exec(t, f.A, "SET TRANSACTION READ ONLY", true, 0)
				stmt, err := f.A.PrepareContext(f.Pair.Ctx, query)
				if err != nil {
					t.Fatal("mode assignment must Prepare", err)
				}
				defer stmt.Close()
				var got string
				if err := f.A.QueryRowContext(f.Pair.Ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != "on" {
					t.Fatal("Prepare changed access mode", got, err)
				}
				_, err = stmt.ExecContext(f.Pair.Ctx)
				want := fixture.ControlsReferenceDiagnostic("25001", "cannot set transaction read-write mode inside a read-only transaction")
				fixture.AssertDiagnostic(t, err, &want)
				_, err = f.A.ExecContext(f.Pair.Ctx, "SELECT v FROM t")
				want = fixture.TransactionAborted()
				fixture.AssertDiagnostic(t, err, &want)
				f.Exec(t, f.A, "ROLLBACK TO known", true, 0)
				if err := f.A.QueryRowContext(f.Pair.Ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != "off" {
					t.Fatal("rollback did not restore parent", got, err)
				}
				f.Exec(t, f.A, "ROLLBACK", false, 0)
			})
		})
	}
}
