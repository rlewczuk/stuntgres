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
			scope := &StatementScope{Op: op, Owner: owner, TxRef: ref}
			if err := ScopeSnapshotPhase(scope, &parser.Select{}); err != nil {
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
				SetScopeSelf(scope, scope)
				owner.Admission.Lock()
				failure := SQLError("22023", "unit admitted failure")
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
	e.Mu.Lock()
	old := e.Catalog.State
	retained := scope.Owner.SequenceCatalog
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	e.Catalog.State.SetEpoch(e.Catalog.State.Epoch() + 1)
	// Wake the candidate as an ordinary resolved safe candidate: admission
	// must still reject its old epoch and discard the retained catalog pointer.
	node.Resolved = true
	close(node.Ready)
	e.Mu.Unlock()
	if retained != old {
		t.Fatal("candidate catalog was not captured")
	}
	if err := join(); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	if scope.Owner.SequenceCatalog != nil || scope.Owner.SnapshotSet {
		t.Fatal("fenced candidate retained old catalog")
	}
}
