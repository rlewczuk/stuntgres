package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
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
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			controlsRequire(t, a, "BEGIN READ WRITE")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			controlsRequire(t, a, "SAVEPOINT parent_mark")
			controlsRequire(t, a, "SET TRANSACTION READ ONLY")
			if tc.nested {
				controlsRequire(t, a, "SAVEPOINT child")
			}
			if tc.failed {
				err := controlsExecute(t, a, "SELECT v FROM missing")
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42P01" || a.TransactionState() != session.TxAborted {
					t.Fatal("failure witness", err)
				}
			}
			ref, ticket := a.Transactions.Ref, a.Transactions.Ticket
			controlsRequire(t, a, tc.end)
			owner := a.Transactions
			if owner.ReadOnly != tc.readOnly {
				t.Fatalf("chained readonly %v want %v", owner.ReadOnly, tc.readOnly)
			}
			if owner.State != session.TxActive || owner.Ref == ref || owner.Ticket == ticket || len(owner.Savepoints) != 0 || owner.SnapshotSet || owner.Command != 0 || owner.Writes != nil {
				t.Fatal("CHAIN did not install clean independent incarnation")
			}
			published := []int64{}
			if tc.end == "COMMIT AND CHAIN" && !tc.failed {
				published = []int64{1}
			}
			enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", published...)
			controlsRequire(t, a, "ROLLBACK")
		})
	}
}
