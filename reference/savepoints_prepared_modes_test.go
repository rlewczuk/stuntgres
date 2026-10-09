package reference_test

import "testing"

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
			savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
				f.exec(t, f.a, "BEGIN ISOLATION LEVEL READ COMMITTED", false, 0)
				f.exec(t, f.a, "SAVEPOINT known", false, 0)
				stmt, err := f.a.PrepareContext(f.pair.ctx, tc.query)
				if err != nil {
					t.Fatal("assignment must Prepare without applying modes", err)
				}
				defer stmt.Close()
				var got string
				if err := f.a.QueryRowContext(f.pair.ctx, "SHOW transaction_isolation").Scan(&got); err != nil || got != "read committed" {
					t.Fatal("Prepare changed isolation", got, err)
				}
				if tc.message != "" {
					_, err = stmt.ExecContext(f.pair.ctx)
					want := controlsReferenceDiagnostic("25001", tc.message)
					referenceAssertDiagnostic(t, err, &want)
					_, err = f.a.ExecContext(f.pair.ctx, "SELECT v FROM t")
					want = referenceTransactionAborted()
					referenceAssertDiagnostic(t, err, &want)
					f.exec(t, f.a, "ROLLBACK TO known", true, 0)
				} else if tc.value {
					if err := stmt.QueryRowContext(f.pair.ctx).Scan(&got); err != nil || got != "read committed" {
						t.Fatal("idempotent prepared query", got, err)
					}
				} else {
					result, err := stmt.ExecContext(f.pair.ctx)
					if err != nil {
						t.Fatal("idempotent prepared assignment", err)
					}
					referenceLockAffected(t, result, 0)
				}
				if err := f.a.QueryRowContext(f.pair.ctx, "SHOW transaction_isolation").Scan(&got); err != nil || got != "read committed" {
					t.Fatal(got, err)
				}
				f.exec(t, f.a, "ROLLBACK", false, 0)
				f.noticesWant(t, "", "")
			})
		})
	}
	for _, query := range []string{"SET TRANSACTION READ WRITE", "SET transaction_read_only=off", "SELECT set_config('transaction_read_only','off',false)"} {
		t.Run("read_write_precedence/"+query, func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
				f.exec(t, f.a, "BEGIN READ WRITE", false, 0)
				f.exec(t, f.a, "SAVEPOINT known", false, 0)
				f.exec(t, f.a, "SET TRANSACTION READ ONLY", true, 0)
				stmt, err := f.a.PrepareContext(f.pair.ctx, query)
				if err != nil {
					t.Fatal("mode assignment must Prepare", err)
				}
				defer stmt.Close()
				var got string
				if err := f.a.QueryRowContext(f.pair.ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != "on" {
					t.Fatal("Prepare changed access mode", got, err)
				}
				_, err = stmt.ExecContext(f.pair.ctx)
				want := controlsReferenceDiagnostic("25001", "cannot set transaction read-write mode inside a read-only transaction")
				referenceAssertDiagnostic(t, err, &want)
				_, err = f.a.ExecContext(f.pair.ctx, "SELECT v FROM t")
				want = referenceTransactionAborted()
				referenceAssertDiagnostic(t, err, &want)
				f.exec(t, f.a, "ROLLBACK TO known", true, 0)
				if err := f.a.QueryRowContext(f.pair.ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != "off" {
					t.Fatal("rollback did not restore parent", got, err)
				}
				f.exec(t, f.a, "ROLLBACK", false, 0)
			})
		})
	}
}
