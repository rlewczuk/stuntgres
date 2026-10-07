package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestRelationDeadlockLifecycleExpiredPredecessor(t *testing.T) {
	for _, statement := range []bool{false, true} {
		t.Run(map[bool]string{false: "lock", true: "statement"}[statement], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE x(v INT)")
			sessionSQL(t, a, "CREATE TABLE y(v INT)")
			transactionBegin(t, a)
			transactionBegin(t, b)
			at, bt := a.transactions.ticket, b.transactions.ticket
			t.Cleanup(func() { e.locks.end(at); e.locks.end(bt) })
			x := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "x")}
			y := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "y")}
			deadlockRequest(t, e, at, x, accessExclusive)
			deadlockRequest(t, e, bt, y, accessExclusive)
			r, err := e.locks.startAcquire(context.Background(), at, y, accessExclusive, relationLockPolicy{timeout: time.Hour})
			if err != nil || r == nil {
				t.Fatal(err)
			}
			// Stop delivery and independently advance the absolute deadline. This proves
			// normalization, rather than relying on a timer goroutine winning a race.
			e.locks.mu.Lock()
			r.timer.Stop()
			expired := time.Now().Add(-time.Second)
			if statement {
				r.policy.statementDeadline = expired
			} else {
				r.deadline = expired
			}
			e.locks.mu.Unlock()
			follower := deadlockRequest(t, e, bt, x, accessExclusive)
			code, message := "55P03", "canceling statement due to lock timeout"
			if statement {
				code, message = "57014", "canceling statement due to statement timeout"
			}
			waitsDiagnostic(t, deadlockResult(t, r), code, message)
			e.locks.mu.Lock()
			terminal := follower.terminal
			e.locks.mu.Unlock()
			if terminal {
				t.Fatal("expired predecessor produced false cycle")
			}
			e.locks.end(at)
			if err := deadlockResult(t, follower); err != nil {
				t.Fatal(err)
			}
			e.locks.end(bt)
			lockEmpty(t, e)
		})
	}
}

func TestRelationDeadlockLifecycleNowaitAndGrant(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE x(v INT)")
	sessionSQL(t, a, "CREATE TABLE y(v INT)")
	transactionBegin(t, a)
	transactionBegin(t, b)
	at, bt := a.transactions.ticket, b.transactions.ticket
	t.Cleanup(func() { e.locks.end(at); e.locks.end(bt) })
	x := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "x")}
	y := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "y")}
	deadlockRequest(t, e, at, x, accessExclusive)
	deadlockRequest(t, e, bt, y, accessExclusive)
	if r, err := e.locks.startAcquire(context.Background(), at, y, accessExclusive, relationLockPolicy{nowait: true}); r != nil || !errors.Is(err, errRelationUnavailable) {
		t.Fatal("NOWAIT must not enqueue", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r, err := e.locks.startAcquire(ctx, bt, x, accessExclusive, relationLockPolicy{})
	if err != nil || r == nil {
		t.Fatal(err)
	}
	e.locks.end(at)
	if err := deadlockResult(t, r); err != nil {
		t.Fatal("acyclic grant", err)
	}
	cancel()
	e.locks.end(bt)
	if err := deadlockResult(t, r); err != nil {
		t.Fatal("later cancellation overwrote grant", err)
	}
	lockEmpty(t, e)
	// A later real transaction incarnation is independent of the ended requests.
	transactionEnd(t, a, at.ref, false)
	transactionEnd(t, b, bt.ref, false)
	transactionBegin(t, a)
	next := a.transactions.ticket
	if next == at || next.ref == at.ref {
		t.Fatal("transaction incarnation reused")
	}
	deadlockRequest(t, e, next, x, accessExclusive)
	deadlockRequest(t, e, next, y, accessExclusive)
	e.locks.end(at)
	e.locks.end(bt)
	e.locks.mu.Lock()
	held := e.locks.entries[x].holders[next]&accessExclusive != 0 && e.locks.entries[y].holders[next]&accessExclusive != 0
	e.locks.mu.Unlock()
	if !held {
		t.Fatal("old end affected fresh incarnation")
	}
	e.locks.end(next)
	lockEmpty(t, e)
}

// SQLSTATE must remain a SQL diagnostic through command admission translation.
func TestRelationDeadlockCommandErrorIdentity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	ctx, cancel := context.WithCancel(context.Background())
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	original := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected"}
	cancel()
	if got := op.CommandError(original); got != original {
		t.Fatalf("genuine SQL error translated: %v", got)
	}
}
