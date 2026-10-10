package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"testing"
)

func TestPGSequenceSnapshotTerminalRelease(t *testing.T) {
	for _, terminal := range []string{"commit", "abort", "session_end"} {
		t.Run(terminal, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			ref, err := e.Begin(op, TxOptions{Isolation: RepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			owner := op.session.transactions
			scope := &StatementScope{op: op, owner: owner, txRef: ref}
			if err := scope.snapshotPhase(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			if owner.sequenceCatalog == nil {
				t.Fatal("snapshot not acquired")
			}
			switch terminal {
			case "commit":
				if _, err := e.Commit(op, ref); err != nil {
					t.Fatal(err)
				}
			case "abort":
				scope.self = scope
				owner.admission.Lock()
				failure := sqlError("22023", "unit admitted failure")
				if err := scope.Finish(failure); err != failure {
					t.Fatal(err)
				}
				if owner.state != TxAborted {
					t.Fatal("failure did not abort owner")
				}
			case "session_end":
				op.Done()
				op.session.RequestEnd(CleanClose)
				if err := op.session.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if owner.sequenceCatalog != nil {
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
	old := e.state
	retained := scope.owner.sequenceCatalog
	e.state = cloneCatalog(e.state)
	e.state.epoch++
	// Wake the candidate as an ordinary resolved safe candidate: admission
	// must still reject its old epoch and discard the retained catalog pointer.
	node.resolved = true
	close(node.ready)
	e.mu.Unlock()
	if retained != old {
		t.Fatal("candidate catalog was not captured")
	}
	if err := join(); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal(err)
	}
	if scope.owner.sequenceCatalog != nil || scope.owner.snapshotSet {
		t.Fatal("fenced candidate retained old catalog")
	}
}
