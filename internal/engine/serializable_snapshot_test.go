package engine

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
)

func serializableUnitAcquisition(t *testing.T, e *core.Engine, parent context.Context, readOnly, deferrable bool) (*core.StatementScope, *visibility.SerializableTx) {
	t.Helper()
	sess := sessionStart(t, e, nil)
	op, err := sess.Acquire(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	ref, err := e.Begin(op, session.TxOptions{Isolation: session.Serializable, ReadOnly: readOnly, Deferrable: deferrable, DeferrableSet: true})
	if err != nil {
		t.Fatal(err)
	}
	return &core.StatementScope{Owner: sess.Transactions, Op: op, TxRef: ref}, e.Visibility.Serializable.Nodes[ref]
}

func serializableUnitAwaitCandidate(t *testing.T, e *core.Engine, node *visibility.SerializableTx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		e.Mu.Lock()
		ready := node.Ready != nil && !node.Resolved && len(node.Possible) > 0
		e.Mu.Unlock()
		if ready {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("candidate registration", err)
		}
		runtime.Gosched()
	}
}

func serializableUnitCaptureWorker(t *testing.T, scope *core.StatementScope) func() error {
	t.Helper()
	done := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		if !joined {
			scope.Op.Session.RequestEnd(session.CleanClose)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("safe capture cleanup did not join")
			}
		}
	})
	go func() {
		scope.Owner.Mu.Lock()
		err := scope.AcquireSerializableSnapshot()
		scope.Owner.Mu.Unlock()
		done <- err
	}()
	return func() error {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("safe capture did not return")
			return nil
		}
	}
}
