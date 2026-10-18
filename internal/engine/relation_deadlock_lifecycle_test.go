package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestRelationDeadlockGraphFeeder(t *testing.T) {
	f := newLockFixture(t)
	tickets := []*relationTicket{f.tx(), f.tx(), f.tx()}
	x := f.relation()
	y := f.relation()
	f.request(tickets[0], x, accessExclusive)
	f.request(tickets[1], y, accessExclusive)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	pending := make([]*relationRequest, 2)
	// A disconnected already-live A<->B cycle must be diagnosed on an actual
	// member, not C merely because C's new request reaches that cycle.
	f.m.mu.Lock()
	for i, resource := range []lockResource{y, x} {
		entry := f.m.entries[resource]
		r := &relationRequest{ticket: tickets[i], provenance: tickets[i].Ref(), ctx: ctx, mode: accessExclusive, done: make(chan struct{}), ref: resource, entry: entry}
		entry.pending = append(entry.pending, r)
		tickets[i].resources[resource] = struct{}{}
		pending[i] = r
	}
	f.m.mu.Unlock()
	feeder := f.request(tickets[2], x, accessExclusive)
	f.m.mu.Lock()
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
	f.m.mu.Unlock()
	if victim < 0 || feederTerminal {
		t.Fatal("victim must be an actual A/B cycle member, not feeder C")
	}
	var d *sqlstate.Error
	if err := f.result(pending[victim]); !errors.As(err, &d) || d.Code != "40P01" {
		t.Fatal("cycle member diagnostic", err)
	}
	f.m.End(tickets[victim])
	if err := f.result(pending[1-victim]); err != nil {
		t.Fatal(err)
	}
	f.m.End(tickets[1-victim])
	if err := f.result(feeder); err != nil {
		t.Fatal(err)
	}
	f.m.End(tickets[2])
	f.empty()
}

func TestRelationDeadlockLifecycleFirstTerminal(t *testing.T) {
	f := newLockFixture(t)
	at, bt := f.tx(), f.tx()
	x := f.relation()
	y := f.relation()
	f.request(at, x, accessExclusive)
	f.request(bt, y, accessExclusive)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	one, err := f.m.StartAcquire(ctx, at, y, accessExclusive, relationLockPolicy{Timeout: time.Minute})
	if err != nil || one == nil {
		t.Fatal(err)
	}
	two := f.request(bt, x, accessExclusive)
	f.m.mu.Lock()
	victim := two
	survivor := one
	victimTicket := bt
	if one.terminal {
		victim = one
		survivor = two
		victimTicket = at
	}
	f.m.mu.Unlock()
	original := f.result(victim)
	var d *sqlstate.Error
	if !errors.As(original, &d) || d.Code != "40P01" {
		t.Fatal(original)
	}
	// Explicitly trigger cancellation/end/epoch retirement after the first
	// terminal result. None may replace the error or double-close notification.
	cancel()
	f.m.End(victimTicket)
	f.m.Fence(at.Ref().Epoch() + 1)
	if again := f.result(victim); again != original {
		t.Fatalf("first terminal error replaced: %v -> %v", original, again)
	}
	select {
	case <-survivor.Done():
	case <-time.After(time.Second):
		t.Fatal("survivor terminal cleanup missing")
	}
	f.empty()
	copied := *at
	if _, err := f.m.StartAcquire(context.Background(), &copied, x, accessShare, relationLockPolicy{}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("retired copied ticket accepted", err)
	}
}

func TestRelationDeadlockGraphAllHolders(t *testing.T) {
	f := newLockFixture(t)
	at, bt, ct := f.tx(), f.tx(), f.tx()
	x := f.relation()
	y := f.relation()
	f.request(at, x, accessShare)
	f.request(bt, x, accessShare)
	f.request(ct, y, accessExclusive)
	cr := f.request(ct, x, accessExclusive)
	ar := f.request(at, y, accessExclusive)
	f.m.mu.Lock()
	av, cv := ar.terminal, cr.terminal
	f.m.mu.Unlock()
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
	if err := f.result(victim); !errors.As(err, &d) || d.Code != "40P01" {
		t.Fatal(err)
	}
	f.m.End(victimTicket)
	f.m.End(bt)
	if err := f.result(survivor); err != nil {
		t.Fatal(err)
	}
	f.m.End(survivorTicket)
	f.empty()
}
