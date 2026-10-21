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
	e.mu.Lock()
	node := e.visibility.Serializable.Nodes[ref]
	node.Captured = true
	node.ReadOnly = readOnly
	node.Frontier = e.visibility.CommitSeq()
	node.SnapshotOrder = e.visibility.SerializableOrder()
	e.mu.Unlock()
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
			e.mu.Lock()
			defer e.mu.Unlock()
			if err := e.visibility.SerializableEdgeLocked(a, a, b); err != nil {
				t.Fatal(err)
			}
			if err := e.visibility.SerializableEdgeLocked(b, b, a); err != nil {
				t.Fatal(err)
			}
			if err := e.visibility.SerializablePrecommitLocked(first.Ref); err != nil {
				t.Fatal(err)
			}
			e.visibility.SerializableCommittedLocked(first.Ref, e.visibility.CommitSeq())
			if !last.Doomed || last.Committed || session.TxOwnerOf(last.Ref).State != session.TxActive || first.Doomed {
				t.Fatal("peer doom became an asynchronous TX abort")
			}
			serializableUnitDiagnostic(t, e.visibility.SerializablePrecommitLocked(last.Ref), "Canceled on identification as a pivot, during commit attempt.")
		})
	}
}
