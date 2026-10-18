package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestRelationDeadlockGraphFeeder(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	c := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE x(v INT)")
	sessionSQL(t, a, "CREATE TABLE y(v INT)")
	for _, s := range []*Session{a, b, c} {
		transactionBegin(t, s)
		ticket := s.transactions.ticket
		t.Cleanup(func() { e.endLockOwner(ticket) })
	}
	tickets := []*relationTicket{a.transactions.ticket, b.transactions.ticket, c.transactions.ticket}
	x := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "x")}
	y := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "y")}
	deadlockRequest(t, e, tickets[0], x, accessExclusive)
	deadlockRequest(t, e, tickets[1], y, accessExclusive)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	pending := make([]*relationRequest, 2)
	// A disconnected already-live A<->B cycle must be diagnosed on an actual
	// member, not C merely because C's new request reaches that cycle.
	e.locks.mu.Lock()
	for i, resource := range []lockResource{y, x} {
		entry := e.locks.entries[resource]
		r := &relationRequest{ticket: tickets[i], provenance: tickets[i].Ref(), ctx: ctx, mode: accessExclusive, done: make(chan struct{}), ref: resource, entry: entry}
		entry.pending = append(entry.pending, r)
		tickets[i].resources[resource] = struct{}{}
		pending[i] = r
	}
	e.locks.mu.Unlock()
	feeder := deadlockRequest(t, e, tickets[2], x, accessExclusive)
	e.locks.mu.Lock()
	victim := -1
	for i, r := range pending {
		if r.terminal {
			if victim >= 0 {
				t.Error("two victims")
			}
			victim = i
		}
	}
	feederTerminal := feeder.terminal
	e.locks.mu.Unlock()
	if victim < 0 || feederTerminal {
		t.Fatal("victim must be an actual A/B cycle member, not feeder C")
	}
	var d *sqlstate.Error
	if err := deadlockResult(t, pending[victim]); !errors.As(err, &d) || d.Code != "40P01" {
		t.Fatal("cycle member diagnostic", err)
	}
	e.endLockOwner(tickets[victim])
	if err := deadlockResult(t, pending[1-victim]); err != nil {
		t.Fatal(err)
	}
	e.endLockOwner(tickets[1-victim])
	if err := deadlockResult(t, feeder); err != nil {
		t.Fatal(err)
	}
	e.endLockOwner(tickets[2])
	lockEmpty(t, e)
}

func TestRelationDeadlockLifecycleFirstTerminal(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE x(v INT)")
	sessionSQL(t, a, "CREATE TABLE y(v INT)")
	transactionBegin(t, a)
	transactionBegin(t, b)
	at, bt := a.transactions.ticket, b.transactions.ticket
	t.Cleanup(func() { e.endLockOwner(at); e.endLockOwner(bt) })
	x := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "x")}
	y := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "y")}
	deadlockRequest(t, e, at, x, accessExclusive)
	deadlockRequest(t, e, bt, y, accessExclusive)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	one, err := e.locks.StartAcquire(ctx, at, y, accessExclusive, relationLockPolicy{Timeout: time.Minute})
	if err != nil || one == nil {
		t.Fatal(err)
	}
	two := deadlockRequest(t, e, bt, x, accessExclusive)
	e.locks.mu.Lock()
	victim := two
	survivor := one
	victimTicket := bt
	if one.terminal {
		victim = one
		survivor = two
		victimTicket = at
	}
	e.locks.mu.Unlock()
	original := deadlockResult(t, victim)
	var d *sqlstate.Error
	if !errors.As(original, &d) || d.Code != "40P01" {
		t.Fatal(original)
	}
	// Explicitly trigger cancellation/end/epoch retirement after the first
	// terminal result. None may replace the error or double-close notification.
	cancel()
	e.endLockOwner(victimTicket)
	e.locks.Fence(at.Ref().Epoch() + 1)
	if again := deadlockResult(t, victim); again != original {
		t.Fatalf("first terminal error replaced: %v -> %v", original, again)
	}
	select {
	case <-survivor.Done():
	case <-time.After(time.Second):
		t.Fatal("survivor terminal cleanup missing")
	}
	lockEmpty(t, e)
	copied := *at
	if _, err := e.locks.StartAcquire(context.Background(), &copied, x, accessShare, relationLockPolicy{}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("retired copied ticket accepted", err)
	}
}

func TestRelationDeadlockGraphAllHolders(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	c := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE x(v INT)")
	sessionSQL(t, a, "CREATE TABLE y(v INT)")
	for _, s := range []*Session{a, b, c} {
		transactionBegin(t, s)
		ticket := s.transactions.ticket
		t.Cleanup(func() { e.endLockOwner(ticket) })
	}
	at, bt, ct := a.transactions.ticket, b.transactions.ticket, c.transactions.ticket
	x := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "x")}
	y := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "y")}
	deadlockRequest(t, e, at, x, accessShare)
	deadlockRequest(t, e, bt, x, accessShare)
	deadlockRequest(t, e, ct, y, accessExclusive)
	cr := deadlockRequest(t, e, ct, x, accessExclusive)
	ar := deadlockRequest(t, e, at, y, accessExclusive)
	e.locks.mu.Lock()
	av, cv := ar.terminal, cr.terminal
	e.locks.mu.Unlock()
	if av == cv {
		t.Fatal("exactly one A/C cycle member must fail")
	}
	victim, survivor := ar, cr
	victimTicket, survivorTicket := at, ct
	if cv {
		victim, survivor = cr, ar
		victimTicket, survivorTicket = ct, at
	}
	var d *sqlstate.Error
	if err := deadlockResult(t, victim); !errors.As(err, &d) || d.Code != "40P01" {
		t.Fatal(err)
	}
	e.endLockOwner(victimTicket)
	e.endLockOwner(bt)
	if err := deadlockResult(t, survivor); err != nil {
		t.Fatal(err)
	}
	e.endLockOwner(survivorTicket)
	lockEmpty(t, e)
}
