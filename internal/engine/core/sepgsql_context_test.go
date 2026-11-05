package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine/enginetest"
)

// Capture the real owner's entry-check result before pausing. Returning that
// captured nil after cancellation lets execution reach its actual final fence.
// All unrelated Err checks delegate normally; no incidental call count selects
// the boundary, and no production hook or audit callback participates.
type sepgsqlContextEntry struct {
	context.Context
	entered, release chan struct{}
	paused           atomic.Bool
}

func (c *sepgsqlContextEntry) Err() error {
	var pcs [64]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	owner := false
	for {
		frame, more := frames.Next()
		owner = owner || strings.HasSuffix(frame.Function, ".(*StatementScope).invokeSepgsql")
		if !more {
			break
		}
	}
	observed := c.Context.Err()
	if owner && c.paused.CompareAndSwap(false, true) {
		close(c.entered)
		select {
		case <-c.release:
		case <-c.Context.Done():
			// The test still owns Engine.Mu until it has released the entry
			// check. Cancellation therefore cannot race past the lock fence.
		}
	}
	return observed
}

func TestSepgsqlSimulationContextCancellation(t *testing.T) {
	e := sepgsqlPhaseEngine(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	peer, peerDone := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(peerDone)
	enginetest.RoleExecute(t, e, op, "BEGIN")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	base := op.Context()
	ctx, cancel := context.WithTimeout(base, 30*time.Second)
	barrier := &sepgsqlContextEntry{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
	op.Ctx = barrier
	statement := enginetest.Statement(t, "SELECT sepgsql_setcon('R')")
	finished := make(chan error, 1)
	var release sync.Once
	joined, locked, cleaned := false, false, false
	t.Cleanup(func() {
		cancel()
		release.Do(func() { close(barrier.release) })
		if locked {
			e.Mu.Unlock()
		}
		if !joined {
			<-finished
		}
		if !cleaned {
			_ = scope.Finish(context.Canceled)
		}
		op.Ctx = base
	})
	go func() {
		result, failure := scope.Execute(statement, 0, nil)
		if result.Rows != nil {
			if closeErr := result.Rows.Close(); failure == nil {
				failure = closeErr
			}
		}
		// Deliberately do not Finish: transaction cleanup must not conceal Set.
		finished <- failure
	}()
	select {
	case <-barrier.entered:
	case err = <-finished:
		joined = true
		t.Fatalf("actual invokeSepgsql entry not reached: %v", err)
	case <-ctx.Done():
		t.Fatal("actual invokeSepgsql entry timed out")
	}
	e.Mu.Lock()
	locked = true
	subject := op.Session.Sepgsql
	mark := subject.Mark()
	current, generation := subject.Snapshot()
	if current != "A" {
		t.Fatal("unexpected initial subject", current)
	}
	cancel()
	release.Do(func() { close(barrier.release) })
	e.Mu.Unlock()
	locked = false
	err = <-finished
	joined = true
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("final fence returned %T %v", err, err)
	}
	if got, gen := subject.Snapshot(); got != current || gen != generation || subject.Mark() != mark {
		t.Fatal("canceled owner executed Set before statement cleanup", got, gen, generation)
	}
	if err = scope.Finish(err); !errors.Is(err, context.Canceled) {
		t.Fatal("statement cancellation cleanup", err)
	}
	cleaned = true
	op.Ctx = base
	enginetest.RoleExecute(t, e, op, "ROLLBACK")
	sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"A"}})
	sepgsqlPhaseRows(t, e, peer, "SELECT sepgsql_getcon()", [][]driver.Value{{"A"}})
	// Same installed SQL without cancellation genuinely mutates this owner.
	sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_setcon('R')", [][]driver.Value{{true}})
	sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
	sepgsqlPhaseRows(t, e, peer, "SELECT sepgsql_getcon()", [][]driver.Value{{"A"}})
}
