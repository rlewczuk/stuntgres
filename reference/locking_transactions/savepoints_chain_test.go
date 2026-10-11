package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

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
			savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
				f.Exec(t, f.A, "BEGIN READ WRITE", false, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
				f.Exec(t, f.A, "SAVEPOINT parent_mark", false, 0)
				f.Exec(t, f.A, "SET TRANSACTION READ ONLY", false, 0)
				if tc.nested {
					f.Exec(t, f.A, "SAVEPOINT child", false, 0)
				}
				if tc.failed {
					_, err := f.A.ExecContext(f.Pair.Ctx, "SELECT v FROM missing")
					want := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
					want.Position = "15"
					fixture.AssertDiagnostic(t, err, &want)
					_, err = f.A.ExecContext(f.Pair.Ctx, "SHOW transaction_read_only")
					want = fixture.TransactionAborted()
					fixture.AssertDiagnostic(t, err, &want)
				}
				f.Exec(t, f.A, tc.end, true, 0)
				var got string
				if err := f.A.QueryRowContext(f.Pair.Ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != tc.want {
					t.Fatalf("chained mode %q want %q: %v", got, tc.want, err)
				}
				published := []int64{}
				if tc.end == "COMMIT AND CHAIN" && !tc.failed {
					published = []int64{1}
				}
				f.Rows(t, f.B, published...)
				if tc.want == "off" {
					f.Exec(t, f.A, "INSERT INTO t VALUES(3)", false, 1)
					f.Exec(t, f.A, "COMMIT", false, 0)
					f.Rows(t, f.B, 3)
				} else {
					_, err := f.A.ExecContext(f.Pair.Ctx, "INSERT INTO t VALUES(3)")
					want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute INSERT in a read-only transaction")
					fixture.AssertDiagnostic(t, err, &want)
					f.Exec(t, f.A, "ROLLBACK", false, 0)
				}
				f.NoticesWant(t, "", "")
			})
		})
	}
}
