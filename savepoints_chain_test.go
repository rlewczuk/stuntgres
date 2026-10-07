package stuntgres_test

import (
	"context"
	"testing"
)

func TestSavepointsChainCharacteristics(t *testing.T) {
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
			_, a, b, notices := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "BEGIN READ WRITE", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			transactionAffected(t, ctx, a, "SAVEPOINT parent_mark", false, 0)
			transactionAffected(t, ctx, a, "SET TRANSACTION READ ONLY", false, 0)
			if tc.nested {
				transactionAffected(t, ctx, a, "SAVEPOINT child", false, 0)
			}
			if tc.failed {
				_, err := a.ExecContext(ctx, "SELECT v FROM missing")
				want := controlsDiagnostic("42P01", `relation "missing" does not exist`)
				want.Position = "15"
				transactionDiagnostic(t, err, want)
				_, err = a.ExecContext(ctx, "SHOW transaction_read_only")
				transactionDiagnostic(t, err, controlsAborted())
			}
			transactionAffected(t, ctx, a, tc.end, true, 0)
			var got string
			if err := a.QueryRowContext(ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != tc.want {
				t.Fatalf("chained mode %q want %q: %v", got, tc.want, err)
			}
			published := []int64{}
			if tc.end == "COMMIT AND CHAIN" && !tc.failed {
				published = []int64{1}
			}
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, published...)
			if tc.want == "off" {
				transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", false, 1)
				transactionAffected(t, ctx, a, "COMMIT", false, 0)
				transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 3)
			} else {
				_, err := a.ExecContext(ctx, "INSERT INTO t VALUES(3)")
				transactionDiagnostic(t, err, controlsDiagnostic("25006", "cannot execute INSERT in a read-only transaction"))
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
			}
			controlsNotices(t, notices, "", "")
		})
	}
}
