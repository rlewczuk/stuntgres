//go:build reference

package reference_test

import "testing"

func TestReferenceSavepointsChainCharacteristics(t *testing.T) {
	for _, tc := range []struct {
		name, end, want string
		nested, failed  bool
	}{
		{"live_rollback", "ROLLBACK AND CHAIN", "off", false, false},
		{"nested_rollback", "ROLLBACK AND CHAIN", "off", true, false},
		{"failed_child_commit", "COMMIT AND CHAIN", "off", true, true},
		{"failed_child_rollback", "ROLLBACK AND CHAIN", "off", true, true},
		{"successful_commit", "COMMIT AND CHAIN", "on", false, false},
		{"successful_nested_commit", "COMMIT AND CHAIN", "on", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
				f.exec(t, f.a, "BEGIN READ WRITE", false, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
				f.exec(t, f.a, "SAVEPOINT parent_mark", false, 0)
				f.exec(t, f.a, "SET TRANSACTION READ ONLY", false, 0)
				if tc.nested {
					f.exec(t, f.a, "SAVEPOINT child", false, 0)
				}
				if tc.failed {
					_, err := f.a.ExecContext(f.pair.ctx, "SELECT v FROM missing")
					want := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
					want.Position = "15"
					referenceAssertDiagnostic(t, err, &want)
					_, err = f.a.ExecContext(f.pair.ctx, "SHOW transaction_read_only")
					want = referenceTransactionAborted()
					referenceAssertDiagnostic(t, err, &want)
				}
				f.exec(t, f.a, tc.end, true, 0)
				var got string
				if err := f.a.QueryRowContext(f.pair.ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != tc.want {
					t.Fatalf("chained mode %q want %q: %v", got, tc.want, err)
				}
				published := []int64{}
				if tc.end == "COMMIT AND CHAIN" && !tc.failed {
					published = []int64{1}
				}
				f.rows(t, f.b, published...)
				if tc.want == "off" {
					f.exec(t, f.a, "INSERT INTO t VALUES(3)", false, 1)
					f.exec(t, f.a, "COMMIT", false, 0)
					f.rows(t, f.b, 3)
				} else {
					_, err := f.a.ExecContext(f.pair.ctx, "INSERT INTO t VALUES(3)")
					want := controlsReferenceDiagnostic("25006", "cannot execute INSERT in a read-only transaction")
					referenceAssertDiagnostic(t, err, &want)
					f.exec(t, f.a, "ROLLBACK", false, 0)
				}
				f.noticesWant(t, "", "")
			})
		})
	}
}
