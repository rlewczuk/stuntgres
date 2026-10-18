package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/ids"
)

func advisoryXactUnitTicket(s *Session, xact bool) *relationTicket {
	if xact {
		return s.transactions.ticket
	}
	return s.advisory.ticket
}
func advisoryXactUnitAcquire(t *testing.T, s *Session, key lockResource, mode relationLockMode, xact, nowait bool) (*relationRequest, error) {
	t.Helper()
	return s.Engine().locks.startAcquire(context.Background(), advisoryXactUnitTicket(s, xact), key, mode, relationLockPolicy{provenance: s.transactions.ref, nowait: nowait})
}
func advisoryXactUnitGrant(t *testing.T, s *Session, key lockResource, mode relationLockMode, xact bool) {
	t.Helper()
	r, err := advisoryXactUnitAcquire(t, s, key, mode, xact, true)
	if r != nil || err != nil {
		t.Fatal("expected immediate grant", r, err)
	}
}
func advisoryXactUnitHold(s *Session, key lockResource) relationHold {
	m := s.Engine().locks
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.entries[key]; e != nil {
		return e.holders[s.transactions.ticket]
	}
	return relationHold{}
}

func TestAdvisoryXactOwnershipCompatibility(t *testing.T) {
	for _, kind := range []lockResourceKind{advisoryBigintResource, advisoryPairResource} {
		for _, holderXact := range []bool{false, true} {
			for _, requestXact := range []bool{false, true} {
				for _, held := range []relationLockMode{advisoryShared, advisoryExclusive} {
					for _, requested := range []relationLockMode{advisoryShared, advisoryExclusive} {
						t.Run(fmt.Sprint(kind, holderXact, requestXact, held, requested), func(t *testing.T) {
							e := New()
							a, _ := advisoryUnitOwner(t, e)
							b, _ := advisoryUnitOwner(t, e)
							key := lockResource{kind: kind, high: 0xffffffff, low: 0x80000000}
							advisoryXactUnitGrant(t, a, key, held, holderXact)
							r, err := advisoryXactUnitAcquire(t, b, key, requested, requestXact, true)
							compatible := held == advisoryShared && requested == advisoryShared
							if r != nil || compatible && err != nil || !compatible && !errors.Is(err, errRelationUnavailable) {
								t.Fatal(r, err, compatible)
							}
							if !compatible {
								m := e.locks
								m.mu.Lock()
								_, journaled := advisoryXactUnitTicket(b, requestXact).resources[key]
								dirty := journaled || len(m.entries[key].pending) != 0 || m.entries[key].holders[advisoryXactUnitTicket(b, requestXact)].mode != 0
								m.mu.Unlock()
								if dirty {
									t.Fatal("try failure changed queue/journal/grants")
								}
							}
						})
					}
				}
			}
		}
	}
	t.Run("cross lifetime priority preserves external grants", func(t *testing.T) {
		for _, xact := range []bool{false, true} {
			e := New()
			a, _ := advisoryUnitOwner(t, e)
			b, _ := advisoryUnitOwner(t, e)
			c, _ := advisoryUnitOwner(t, e)
			key := lockResource{kind: advisoryPairResource, high: 7, low: 8}
			advisoryXactUnitGrant(t, a, key, advisoryShared, xact)
			r, err := advisoryXactUnitAcquire(t, b, key, advisoryExclusive, true, false)
			if err != nil || r == nil {
				t.Fatal(r, err)
			}
			advisoryXactUnitGrant(t, a, key, advisoryShared, !xact)
			advisoryXactUnitGrant(t, a, key, advisoryExclusive, !xact)
			e.endLockOwner(b.transactions.ticket)
			if err := r.wait(); err == nil {
				t.Fatal("ended waiter granted")
			}
			transactionEnd(t, a, a.transactions.ref, false)
			if err := e.locks.unlockAdvisoryAll(a.advisory.ticket); err != nil {
				t.Fatal(err)
			}
			transactionBegin(t, a)
			advisoryXactUnitGrant(t, a, key, advisoryShared, false)
			advisoryXactUnitGrant(t, c, key, advisoryShared, true)
			if _, err := advisoryXactUnitAcquire(t, a, key, advisoryExclusive, true, true); !errors.Is(err, errRelationUnavailable) {
				t.Fatal("ignored external grant", err)
			}
		}
	})
	t.Run("journal overlap and idempotence", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, _ := advisoryUnitOwner(t, e)
		key := lockResource{kind: advisoryBigintResource, low: 32}
		advisoryXactUnitGrant(t, a, key, advisoryShared, true)
		advisoryXactUnitGrant(t, a, key, advisoryShared, true)
		if got := advisoryXactUnitHold(a, key); got != (relationHold{mode: advisoryShared}) {
			t.Fatal("transaction grant counted", got)
		}
		advisoryXactUnitGrant(t, b, key, advisoryShared, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r, err := e.locks.startAcquire(ctx, a.advisory.ticket, key, advisoryExclusive, relationLockPolicy{provenance: ra})
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		cancel()
		if err := r.wait(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		e.locks.mu.Lock()
		_, journaled := a.transactions.ticket.resources[key]
		e.locks.mu.Unlock()
		if !journaled {
			t.Fatal("session terminal erased transaction hold journal")
		}
		e.endLockOwner(a.transactions.ticket)
		if _, err := advisoryXactUnitAcquire(t, b, key, advisoryExclusive, true, true); err != nil {
			t.Fatal("transaction hold leaked", err)
		}
	})
	t.Run("validation and disjoint spaces", func(t *testing.T) {
		e := New()
		a, _ := advisoryUnitOwner(t, e)
		b, _ := advisoryUnitOwner(t, e)
		key := lockResource{kind: advisoryBigintResource, high: 1, low: 2}
		pair := key
		pair.kind = advisoryPairResource
		advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
		advisoryXactUnitGrant(t, b, pair, advisoryExclusive, true)
		for _, bad := range []lockResource{{kind: advisoryPairResource, row: 1}, {kind: advisoryBigintResource, ObjectRef: ids.ObjectRef{Address: ids.ObjectAddress{ObjectID: 1}}}} {
			if _, err := advisoryXactUnitAcquire(t, a, bad, advisoryExclusive, true, true); err == nil {
				t.Fatal("invalid advisory resource accepted", bad)
			}
		}
		other := New()
		foreign, _ := advisoryUnitOwner(t, other)
		if _, err := e.locks.startAcquire(context.Background(), foreign.transactions.ticket, key, advisoryExclusive, relationLockPolicy{}); !errors.Is(err, ErrCatalogReplaced) {
			t.Fatal("foreign owner", err)
		}
		e.locks.detachPrepared(a.transactions.ticket)
		if _, err := advisoryXactUnitAcquire(t, a, pair, advisoryShared, true, true); err == nil {
			t.Fatal("detached requester admitted")
		}
	})
}

func TestAdvisoryXactSavepointLifecycle(t *testing.T) {
	for _, outer := range []relationLockMode{advisoryShared, advisoryExclusive} {
		e := New()
		a, _ := advisoryUnitOwner(t, e)
		b, _ := advisoryUnitOwner(t, e)
		m := e.locks
		ticket := a.transactions.ticket
		key := lockResource{kind: advisoryBigintResource, low: 1}
		child := lockResource{kind: advisoryPairResource, low: 1}
		advisoryXactUnitGrant(t, a, key, outer, true)
		mark := m.checkpoint(ticket)
		advisoryXactUnitGrant(t, a, key, advisoryShared, true)
		advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
		advisoryXactUnitGrant(t, a, child, advisoryExclusive, true)
		advisoryXactUnitGrant(t, a, child, advisoryExclusive, true)
		advisoryXactUnitGrant(t, a, child, advisoryShared, false)
		waiter, err := advisoryXactUnitAcquire(t, b, child, advisoryShared, true, false)
		if err != nil || waiter == nil {
			t.Fatal(waiter, err)
		}
		m.restore(ticket, mark)
		if err := waiter.wait(); err != nil {
			t.Fatal(err)
		}
		if got := advisoryXactUnitHold(a, key); got.mode != outer {
			t.Fatal("earlier mode not restored", got, outer)
		}
		if got := advisoryXactUnitHold(a, child); got.mode != 0 || advisoryUnitHold(a, child).shared != 1 {
			t.Fatal("child restore crossed lifetime", got)
		}
		m.restore(ticket, mark)
		if got := advisoryXactUnitHold(a, key); got.mode != outer {
			t.Fatal("repeat restore changed earlier hold", got)
		}
		m.end(ticket)
		if _, err := advisoryXactUnitAcquire(t, b, key, advisoryExclusive, true, true); err != nil {
			t.Fatal("owner end leaked", err)
		}
	}
	for _, grantBeforeFence := range []bool{false, true} {
		t.Run(fmt.Sprint("fence/", grantBeforeFence), func(t *testing.T) {
			e := New()
			a, ra := advisoryUnitOwner(t, e)
			b, rb := advisoryUnitOwner(t, e)
			key := lockResource{kind: advisoryPairResource, high: 8, low: 9}
			advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
			persistent := lockResource{kind: advisoryBigintResource, low: 88}
			advisoryUnitGrant(t, a, ra, persistent, advisoryShared)
			transactionEnd(t, b, rb, false)
			ticket, _, done := advisoryUnitScopeWait(t, b, key, advisoryTransaction)
			if grantBeforeFence {
				e.replacementMu.Lock()
				reserved := true
				t.Cleanup(func() {
					if reserved {
						e.replacementMu.Unlock()
					}
				})
				e.endLockOwner(a.transactions.ticket)
				e.locks.mu.Lock()
				granted := e.locks.entries[key].holders[ticket].mode == advisoryExclusive
				e.locks.mu.Unlock()
				if !granted {
					t.Fatal("no actual pre-fence grant")
				}
				advisoryUnitReplaceReserved(t, e)
				e.replacementMu.Unlock()
				reserved = false
			} else if err := e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
			advisoryUnitScopeResult(t, done, ErrCatalogReplaced)
			c, _ := advisoryUnitOwner(t, e)
			advisoryXactUnitGrant(t, c, key, advisoryExclusive, true)
			if advisoryUnitHold(a, persistent).shared != 1 {
				t.Fatal("fence released session hold")
			}
		})
	}
}
