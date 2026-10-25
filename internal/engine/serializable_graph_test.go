package engine

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

func serializableUnitNode(t *testing.T, e *Engine, readOnly bool) *visibility.SerializableTx {
	t.Helper()
	s := sessionStart(t, e, nil)
	op := transactionOperation(t, s)
	ref, err := e.Begin(op, session.TxOptions{Isolation: session.Serializable, ReadOnly: readOnly})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	node := e.Visibility.Serializable.Nodes[ref]
	node.Captured = true
	node.ReadOnly = readOnly
	node.Frontier = e.Visibility.CommitSeq()
	node.SnapshotOrder = e.Visibility.SerializableOrder()
	e.Mu.Unlock()
	return node
}
func serializableUnitDiagnostic(t *testing.T, err error, detail string) {
	t.Helper()
	var got *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to read/write dependencies among transactions", Detail: "Reason code: " + detail, Hint: "The transaction might succeed if retried."}
	if !errors.As(err, &got) || *got != *want {
		t.Fatalf("diagnostic got %#v (%v), want %#v", got, err, want)
	}
}
func TestSerializableGraphCycleFirstCommitDoomsActivePivot(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			e := New()
			a := serializableUnitNode(t, e, false)
			b := serializableUnitNode(t, e, false)
			first, last := a, b
			if reverse {
				first, last = b, a
			}
			e.Mu.Lock()
			defer e.Mu.Unlock()
			if err := e.Visibility.SerializableEdgeLocked(a, a, b); err != nil {
				t.Fatal(err)
			}
			if err := e.Visibility.SerializableEdgeLocked(b, b, a); err != nil {
				t.Fatal(err)
			}
			if err := e.Visibility.SerializablePrecommitLocked(first.Ref); err != nil {
				t.Fatal(err)
			}
			e.Visibility.SerializableCommittedLocked(first.Ref, e.Visibility.CommitSeq())
			if !last.Doomed || last.Committed || session.TxOwnerOf(last.Ref).State != session.TxActive || first.Doomed {
				t.Fatal("peer doom became an asynchronous TX abort")
			}
			serializableUnitDiagnostic(t, e.Visibility.SerializablePrecommitLocked(last.Ref), "Canceled on identification as a pivot, during commit attempt.")
		})
	}
}
