package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBlockingPidsAdvisoryAuthority(t *testing.T) {
	e := New()
	a, ra := advisoryUnitOwner(t, e)
	b, rb := advisoryUnitOwner(t, e)
	key := lockResource{kind: advisoryBigintResource, low: 187418}
	advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
	m := e.locks
	sessionTicket, source := b.advisory.ticket, b.transactions.ticket
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := m.startAcquire(ctx, sessionTicket, key, advisoryExclusive, relationLockPolicy{provenance: rb})
	if err != nil || r == nil {
		t.Fatal("persistent advisory request", r, err)
	}
	blockingUnitWant(t, e, b.token.ID(), int64(a.token.ID()))
	blockingUnitSafeWant(t, e, b.token.ID())
	// Persistent lookup requires both independent journals, not just a valid
	// physical ticket or a pointer retained in the resource queue.
	m.mu.Lock()
	delete(sessionTicket.resources, key)
	m.mu.Unlock()
	blockingUnitWant(t, e, b.token.ID())
	m.mu.Lock()
	sessionTicket.resources[key] = struct{}{}
	delete(source.resources, key)
	m.mu.Unlock()
	blockingUnitWant(t, e, b.token.ID())
	m.mu.Lock()
	source.resources[key] = struct{}{}
	delete(m.owners, rb)
	m.mu.Unlock()
	blockingUnitWant(t, e, b.token.ID())
	m.mu.Lock()
	m.owners[rb] = source
	unchanged := !r.terminal && r.entry == m.entries[key] && len(r.entry.pending) == 1 && r.entry.pending[0] == r
	m.mu.Unlock()
	if !unchanged {
		t.Fatal("inquiry changed persistent queue")
	}
	blockingUnitWant(t, e, b.token.ID(), int64(a.token.ID()))
	// Canonical transaction abort must terminalize the pending request, while
	// leaving the physical holder and the waiter's session ticket alive.
	transactionEnd(t, b, rb, false)
	if err := deadlockResult(t, r); !errors.Is(err, errRelationOwnerEnded) {
		t.Fatal("aborted provenance", err)
	}
	blockingUnitWant(t, e, b.token.ID())
	if got := advisoryUnitHold(a, key); got.exclusive != 1 {
		t.Fatal("abort released independent holder", got)
	}
	if got := advisoryUnitHold(b, key); got != (relationHold{}) {
		t.Fatal("aborted attempt acquired a count", got)
	}
	fresh := transactionBegin(t, b)
	if fresh == rb || b.advisory.ticket != sessionTicket {
		t.Fatal("wrong transaction/physical lifetime")
	}
	r2, err := m.startAcquire(ctx, sessionTicket, key, advisoryExclusive, relationLockPolicy{provenance: fresh})
	if err != nil || r2 == nil {
		t.Fatal("fresh persistent request", r2, err)
	}
	blockingUnitWant(t, e, b.token.ID(), int64(a.token.ID()))
	// Old provenance is not accepted merely because the same physical session
	// now has another live transaction and a journal for the same advisory key.
	m.mu.Lock()
	r2.provenance = rb
	m.mu.Unlock()
	blockingUnitWant(t, e, b.token.ID())
	m.mu.Lock()
	r2.provenance = fresh
	m.mu.Unlock()
	blockingUnitWant(t, e, b.token.ID(), int64(a.token.ID()))
	advisoryUnitUnlock(t, a, key, advisoryExclusive, true)
	if err := deadlockResult(t, r2); err != nil {
		t.Fatal("fresh transfer", err)
	}
	blockingUnitWant(t, e, b.token.ID())
	m.mu.Lock()
	_, pendingJournal := b.transactions.ticket.resources[key]
	_, heldJournal := sessionTicket.resources[key]
	m.mu.Unlock()
	if pendingJournal || !heldJournal {
		t.Fatal("grant did not retire only provenance journal", pendingJournal, heldJournal)
	}
	if got := advisoryUnitHold(b, key); got.exclusive != 1 {
		t.Fatal("canceled provenance leaked count", got)
	}
	advisoryUnitUnlock(t, b, key, advisoryExclusive, true)
	advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
	advisoryUnitUnlock(t, a, key, advisoryExclusive, true)
	transactionEnd(t, b, fresh, false)
}
