package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestSavepointsChainCharacteristics(t *testing.T) {
	for _, tc := range []struct {
		name, end                string
		readOnly, nested, failed bool
	}{
		{"live_rollback", "ROLLBACK AND CHAIN", false, false, false},
		{"nested_rollback", "ROLLBACK AND CHAIN", false, true, false},
		{"failed_child_commit", "COMMIT AND CHAIN", false, true, true},
		{"failed_child_rollback", "ROLLBACK AND CHAIN", false, true, true},
		{"successful_commit", "COMMIT AND CHAIN", true, false, false},
		{"successful_nested_commit", "COMMIT AND CHAIN", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			controlsRequire(t, a, "BEGIN READ WRITE")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			controlsRequire(t, a, "SAVEPOINT parent_mark")
			controlsRequire(t, a, "SET TRANSACTION READ ONLY")
			if tc.nested {
				controlsRequire(t, a, "SAVEPOINT child")
			}
			if tc.failed {
				err := controlsExecute(t, a, "SELECT v FROM missing")
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42P01" || a.TransactionState() != TxAborted {
					t.Fatal("failure witness", err)
				}
			}
			ref, ticket := a.transactions.ref, a.transactions.ticket
			controlsRequire(t, a, tc.end)
			owner := a.transactions
			if owner.readOnly != tc.readOnly {
				t.Fatalf("chained readonly %v want %v", owner.readOnly, tc.readOnly)
			}
			if owner.state != TxActive || owner.ref == ref || owner.ticket == ticket || len(owner.savepoints) != 0 || owner.snapshotSet || owner.command != 0 || owner.writes != nil {
				t.Fatal("CHAIN did not install clean independent incarnation")
			}
			published := []int64{}
			if tc.end == "COMMIT AND CHAIN" && !tc.failed {
				published = []int64{1}
			}
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", published...)
			controlsRequire(t, a, "ROLLBACK")
		})
	}
}
