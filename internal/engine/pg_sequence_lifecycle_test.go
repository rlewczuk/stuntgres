package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"testing"
)

func TestPGSequenceSnapshotTerminalRelease(t *testing.T) {
	for _, terminal := range []string{"commit", "abort", "session_end"} {
		t.Run(terminal, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			ref, err := e.Begin(op, session.TxOptions{Isolation: session.RepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			owner := op.Session.Transactions
			scope := &StatementScope{op: op, owner: owner, txRef: ref}
			if err := scope.snapshotPhase(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			if owner.SequenceCatalog == nil {
				t.Fatal("snapshot not acquired")
			}
			switch terminal {
			case "commit":
				if _, err := e.Commit(op, ref); err != nil {
					t.Fatal(err)
				}
			case "abort":
				scope.self = scope
				owner.Admission.Lock()
				failure := sqlError("22023", "unit admitted failure")
				if err := scope.Finish(failure); err != failure {
					t.Fatal(err)
				}
				if owner.State != session.TxAborted {
					t.Fatal("failure did not abort owner")
				}
			case "session_end":
				op.Done()
				op.Session.RequestEnd(session.CleanClose)
				if err := op.Session.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if owner.SequenceCatalog != nil {
				t.Fatal("terminal owner retained catalog")
			}
		})
	}
}

func TestPGSequenceSerializableCandidateEpochFence(t *testing.T) {
	e := New()
	serializableUnitNode(t, e, false)
	scope, node := serializableUnitAcquisition(t, e, context.Background(), true, true)
	join := serializableUnitCaptureWorker(t, scope)
	serializableUnitAwaitCandidate(t, e, node)
	e.mu.Lock()
	old := e.catalog.State
	retained := scope.owner.SequenceCatalog
	e.catalog.PublishLocked(catalog.CloneState(e.catalog.State))
	e.catalog.State.SetEpoch(e.catalog.State.Epoch() + 1)
	// Wake the candidate as an ordinary resolved safe candidate: admission
	// must still reject its old epoch and discard the retained catalog pointer.
	node.Resolved = true
	close(node.Ready)
	e.mu.Unlock()
	if retained != old {
		t.Fatal("candidate catalog was not captured")
	}
	if err := join(); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	if scope.owner.SequenceCatalog != nil || scope.owner.SnapshotSet {
		t.Fatal("fenced candidate retained old catalog")
	}
}
