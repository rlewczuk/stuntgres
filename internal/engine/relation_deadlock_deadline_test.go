package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRelationDeadlockLifecycleExpiredPredecessor(t *testing.T) {
	for _, statement := range []bool{false, true} {
		t.Run(map[bool]string{false: "lock", true: "statement"}[statement], func(t *testing.T) {
			f := newLockFixture(t)
			at, bt := f.tx(), f.tx()
			x := f.relation()
			y := f.relation()
			f.request(at, x, accessExclusive)
			f.request(bt, y, accessExclusive)
			r, err := f.m.StartAcquire(context.Background(), at, y, accessExclusive, relationLockPolicy{Timeout: time.Hour})
			if err != nil || r == nil {
				t.Fatal(err)
			}
			// Stop delivery and independently advance the absolute deadline. This proves
			// normalization, rather than relying on a timer goroutine winning a race.
			f.m.mu.Lock()
			r.timer.Stop()
			expired := time.Now().Add(-time.Second)
			if statement {
				r.policy.StatementDeadline = expired
			} else {
				r.deadline = expired
			}
			f.m.mu.Unlock()
			follower := f.request(bt, x, accessExclusive)
			code, message := "55P03", "canceling statement due to lock timeout"
			if statement {
				code, message = "57014", "canceling statement due to statement timeout"
			}
			waitsDiagnostic(t, f.result(r), code, message)
			terminal := follower.Terminal()
			if terminal {
				t.Fatal("expired predecessor produced false cycle")
			}
			f.m.End(at)
			if err := f.result(follower); err != nil {
				t.Fatal(err)
			}
			f.m.End(bt)
			f.empty()
		})
	}
}

func TestRelationDeadlockLifecycleNowaitAndGrant(t *testing.T) {
	f := newLockFixture(t)
	at, bt := f.tx(), f.tx()
	x := f.relation()
	y := f.relation()
	f.request(at, x, accessExclusive)
	f.request(bt, y, accessExclusive)
	if r, err := f.m.StartAcquire(context.Background(), at, y, accessExclusive, relationLockPolicy{NoWait: true}); r != nil || !errors.Is(err, errRelationUnavailable) {
		t.Fatal("NOWAIT must not enqueue", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r, err := f.m.StartAcquire(ctx, bt, x, accessExclusive, relationLockPolicy{})
	if err != nil || r == nil {
		t.Fatal(err)
	}
	f.m.End(at)
	if err := f.result(r); err != nil {
		t.Fatal("acyclic grant", err)
	}
	cancel()
	f.m.End(bt)
	if err := f.result(r); err != nil {
		t.Fatal("later cancellation overwrote grant", err)
	}
	f.empty()
	// A later transaction incarnation of the same session is independent of the
	// ended requests.
	next := f.txIn(at.Session())
	if next == at || next.Ref() == at.Ref() {
		t.Fatal("transaction incarnation reused")
	}
	f.request(next, x, accessExclusive)
	f.request(next, y, accessExclusive)
	f.m.End(at)
	f.m.End(bt)
	lockState := f.m.Inspect()
	held := lockState.Resources[x].Holders[next].Mode&accessExclusive != 0 && lockState.Resources[y].Holders[next].Mode&accessExclusive != 0
	if !held {
		t.Fatal("old end affected fresh incarnation")
	}
	f.m.End(next)
	f.empty()
}
