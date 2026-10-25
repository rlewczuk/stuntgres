package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

func serializableUnitNode(t *testing.T, e *core.Engine, readOnly bool) *visibility.SerializableTx {
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
