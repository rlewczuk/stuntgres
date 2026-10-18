package lock

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

// advisoryUnitParticipant is one session's persistent advisory ticket and the
// live transaction that supplies its requests' provenance.
type advisoryUnitParticipant struct {
	token    ids.SessionToken
	advisory *Ticket
	tx       *Ticket
}

func (f *lockFixture) advisoryParticipant() advisoryUnitParticipant {
	token := f.session()
	return advisoryUnitParticipant{token: token, advisory: f.advisory(token), tx: f.txIn(token)}
}

func (f *lockFixture) advisoryAcquire(p advisoryUnitParticipant, key Resource, mode Mode, nowait bool) (*relationRequest, error) {
	return f.m.StartAcquire(context.Background(), p.advisory, key, mode, Policy{Provenance: p.tx.Ref(), NoWait: nowait})
}

func (f *lockFixture) advisoryGrant(p advisoryUnitParticipant, key Resource, mode Mode) {
	f.t.Helper()
	if r, err := f.advisoryAcquire(p, key, mode, true); err != nil || r != nil {
		f.t.Fatal("immediate grant", r, err)
	}
}

func (f *lockFixture) advisoryHold(p advisoryUnitParticipant, key Resource) Hold {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	if entry := f.m.entries[key]; entry != nil {
		return entry.holders[p.advisory]
	}
	return Hold{}
}

func (f *lockFixture) advisoryUnlock(p advisoryUnitParticipant, key Resource, mode Mode, want bool) {
	f.t.Helper()
	if got, err := f.m.UnlockAdvisory(p.advisory, key, mode); err != nil || got != want {
		f.t.Fatal("exact mode unlock", got, err, want)
	}
}

// blocking fails unless pid's blocking PIDs are exactly want.
func (f *lockFixture) blocking(pid ids.SessionID, want ...int32) {
	f.t.Helper()
	got, err := f.m.BlockingPids(pid)
	if err != nil {
		f.t.Fatal(err)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		f.t.Fatalf("blocking pids for %d = %v, want %v", pid, got, want)
	}
}

func TestAdvisoryCountedGrantsAndOverflow(t *testing.T) {
	f := newLockFixture(t)
	a, b := f.advisoryParticipant(), f.advisoryParticipant()
	big := Resource{Kind: AdvisoryBigintResource, High: 0xffffffff, Low: 0xffffffff}
	pair := big
	pair.Kind = AdvisoryPairResource
	f.advisoryGrant(a, big, AdvisoryShare)
	f.advisoryGrant(a, big, AdvisoryShare)
	f.advisoryGrant(a, big, AdvisoryExclusive)
	f.advisoryGrant(a, big, AdvisoryExclusive)
	f.advisoryGrant(b, pair, AdvisoryExclusive)
	want := Hold{Mode: AdvisoryShare | AdvisoryExclusive, Shared: 2, Exclusive: 2}
	if got := f.advisoryHold(a, big); got != want {
		t.Fatal(got, want)
	}
	f.advisoryUnlock(a, big, AdvisoryExclusive, true)
	f.advisoryUnlock(a, big, AdvisoryExclusive, true)
	f.advisoryUnlock(a, big, AdvisoryExclusive, false)
	if got := f.advisoryHold(a, big); got.Mode != AdvisoryShare || got.Shared != 2 || got.Exclusive != 0 {
		t.Fatal(got)
	}
	f.advisoryGrant(b, big, AdvisoryShare)
	f.advisoryUnlock(b, big, AdvisoryShare, true)
	m := f.m
	m.mu.Lock()
	entry := m.entries[big]
	h := entry.holders[a.advisory]
	h.Shared = ^uint64(0)
	entry.holders[a.advisory] = h
	m.mu.Unlock()
	r, err := f.advisoryAcquire(a, big, AdvisoryShare, true)
	if r != nil || err == nil || err.Error() != "stuntgres: advisory hold count exhausted" || f.advisoryHold(a, big) != h {
		t.Fatal("overflow must not mutate", r, err, f.advisoryHold(a, big))
	}
	if err = m.UnlockAdvisoryAll(a.advisory); err != nil {
		t.Fatal(err)
	}
	if f.advisoryHold(a, big) != (Hold{}) || f.advisoryHold(b, pair).Exclusive != 1 {
		t.Fatal("unlock all scope")
	}
	f.advisoryUnlock(b, pair, AdvisoryExclusive, true)
}

func TestAdvisoryExpiredSourceWinsDelayedCallback(t *testing.T) {
	f := newLockFixture(t)
	a, b := f.advisoryParticipant(), f.advisoryParticipant()
	key := Resource{Kind: AdvisoryBigintResource, Low: 16}
	prior := Resource{Kind: AdvisoryPairResource, High: 16, Low: 17}
	f.advisoryGrant(a, key, AdvisoryExclusive)
	f.advisoryGrant(b, prior, AdvisoryExclusive)
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	t.Cleanup(cancel)
	request, err := f.m.StartAcquire(ctx, b.advisory, key, AdvisoryExclusive, Policy{Source: ctx, Provenance: b.tx.Ref()})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	// The actual callback cannot acquire metadata until after the source expires.
	// Release/settle must consult the source, not callback scheduling order.
	f.m.mu.Lock()
	<-ctx.Done()
	f.m.mu.Unlock()
	f.advisoryUnlock(a, key, AdvisoryExclusive, true)
	if err = f.result(request); !errors.Is(err, context.DeadlineExceeded) || f.advisoryHold(b, key) != (Hold{}) || f.advisoryHold(b, prior).Exclusive != 1 {
		t.Fatal("delayed source callback granted/count leak", err)
	}
}

func TestBlockingPidsAdvisoryAuthority(t *testing.T) {
	f := newLockFixture(t)
	a, b := f.advisoryParticipant(), f.advisoryParticipant()
	key := Resource{Kind: AdvisoryBigintResource, Low: 187418}
	f.advisoryGrant(a, key, AdvisoryExclusive)
	m := f.m
	sessionTicket, source := b.advisory, b.tx
	rb := source.Ref()
	bid, aid := b.token.ID(), int32(a.token.ID())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := m.StartAcquire(ctx, sessionTicket, key, AdvisoryExclusive, Policy{Provenance: rb})
	if err != nil || r == nil {
		t.Fatal("persistent advisory request", r, err)
	}
	f.blocking(bid, aid)
	// Persistent lookup requires both independent journals, not just a valid
	// physical ticket or a pointer retained in the resource queue.
	m.mu.Lock()
	delete(sessionTicket.resources, key)
	m.mu.Unlock()
	f.blocking(bid)
	m.mu.Lock()
	sessionTicket.resources[key] = struct{}{}
	delete(source.resources, key)
	m.mu.Unlock()
	f.blocking(bid)
	m.mu.Lock()
	source.resources[key] = struct{}{}
	delete(m.owners, rb)
	m.mu.Unlock()
	f.blocking(bid)
	m.mu.Lock()
	m.owners[rb] = source
	unchanged := !r.terminal && r.entry == m.entries[key] && len(r.entry.pending) == 1 && r.entry.pending[0] == r
	m.mu.Unlock()
	if !unchanged {
		t.Fatal("inquiry changed persistent queue")
	}
	f.blocking(bid, aid)
	// Ending the transaction must terminalize the pending request, while
	// leaving the physical holder and the waiter's session ticket alive.
	m.End(source)
	if err := f.result(r); !errors.Is(err, ErrOwnerEnded) {
		t.Fatal("aborted provenance", err)
	}
	f.blocking(bid)
	if got := f.advisoryHold(a, key); got.Exclusive != 1 {
		t.Fatal("abort released independent holder", got)
	}
	if got := f.advisoryHold(b, key); got != (Hold{}) {
		t.Fatal("aborted attempt acquired a count", got)
	}
	fresh := f.txIn(b.token)
	if fresh.Ref() == rb {
		t.Fatal("wrong transaction lifetime")
	}
	r2, err := m.StartAcquire(ctx, sessionTicket, key, AdvisoryExclusive, Policy{Provenance: fresh.Ref()})
	if err != nil || r2 == nil {
		t.Fatal("fresh persistent request", r2, err)
	}
	f.blocking(bid, aid)
	// Old provenance is not accepted merely because the same physical session
	// now has another live transaction and a journal for the same advisory key.
	m.mu.Lock()
	r2.provenance = rb
	m.mu.Unlock()
	f.blocking(bid)
	m.mu.Lock()
	r2.provenance = fresh.Ref()
	m.mu.Unlock()
	f.blocking(bid, aid)
	f.advisoryUnlock(a, key, AdvisoryExclusive, true)
	if err := f.result(r2); err != nil {
		t.Fatal("fresh transfer", err)
	}
	f.blocking(bid)
	m.mu.Lock()
	_, pendingJournal := fresh.resources[key]
	_, heldJournal := sessionTicket.resources[key]
	m.mu.Unlock()
	if pendingJournal || !heldJournal {
		t.Fatal("grant did not retire only provenance journal", pendingJournal, heldJournal)
	}
	if got := f.advisoryHold(b, key); got.Exclusive != 1 {
		t.Fatal("canceled provenance leaked count", got)
	}
	f.advisoryUnlock(b, key, AdvisoryExclusive, true)
	f.advisoryGrant(a, key, AdvisoryExclusive)
	f.advisoryUnlock(a, key, AdvisoryExclusive, true)
}
