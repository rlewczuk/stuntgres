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
	return s.Engine().locks.StartAcquire(context.Background(), advisoryXactUnitTicket(s, xact), key, mode, relationLockPolicy{Provenance: s.transactions.ref, NoWait: nowait})
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
							key := lockResource{Kind: kind, High: 0xffffffff, Low: 0x80000000}
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
								dirty := journaled || len(m.entries[key].pending) != 0 || m.entries[key].holders[advisoryXactUnitTicket(b, requestXact)].Mode != 0
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
			key := lockResource{Kind: advisoryPairResource, High: 7, Low: 8}
			advisoryXactUnitGrant(t, a, key, advisoryShared, xact)
			r, err := advisoryXactUnitAcquire(t, b, key, advisoryExclusive, true, false)
			if err != nil || r == nil {
				t.Fatal(r, err)
			}
			advisoryXactUnitGrant(t, a, key, advisoryShared, !xact)
			advisoryXactUnitGrant(t, a, key, advisoryExclusive, !xact)
			e.endLockOwner(b.transactions.ticket)
			if err := r.Wait(); err == nil {
				t.Fatal("ended waiter granted")
			}
			transactionEnd(t, a, a.transactions.ref, false)
			if err := e.locks.UnlockAdvisoryAll(a.advisory.ticket); err != nil {
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
		key := lockResource{Kind: advisoryBigintResource, Low: 32}
		advisoryXactUnitGrant(t, a, key, advisoryShared, true)
		advisoryXactUnitGrant(t, a, key, advisoryShared, true)
		if got := advisoryXactUnitHold(a, key); got != (relationHold{Mode: advisoryShared}) {
			t.Fatal("transaction grant counted", got)
		}
		advisoryXactUnitGrant(t, b, key, advisoryShared, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r, err := e.locks.StartAcquire(ctx, a.advisory.ticket, key, advisoryExclusive, relationLockPolicy{Provenance: ra})
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		cancel()
		if err := r.Wait(); !errors.Is(err, context.Canceled) {
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
		key := lockResource{Kind: advisoryBigintResource, High: 1, Low: 2}
		pair := key
		pair.Kind = advisoryPairResource
		advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
		advisoryXactUnitGrant(t, b, pair, advisoryExclusive, true)
		for _, bad := range []lockResource{{Kind: advisoryPairResource, Row: 1}, {Kind: advisoryBigintResource, ObjectRef: ids.ObjectRef{Address: ids.ObjectAddress{ObjectID: 1}}}} {
			if _, err := advisoryXactUnitAcquire(t, a, bad, advisoryExclusive, true, true); err == nil {
				t.Fatal("invalid advisory resource accepted", bad)
			}
		}
		other := New()
		foreign, _ := advisoryUnitOwner(t, other)
		if _, err := e.locks.StartAcquire(context.Background(), foreign.transactions.ticket, key, advisoryExclusive, relationLockPolicy{}); !errors.Is(err, ErrCatalogReplaced) {
			t.Fatal("foreign owner", err)
		}
		e.locks.DetachPrepared(a.transactions.ticket)
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
		key := lockResource{Kind: advisoryBigintResource, Low: 1}
		child := lockResource{Kind: advisoryPairResource, Low: 1}
		advisoryXactUnitGrant(t, a, key, outer, true)
		mark := m.Checkpoint(ticket)
		advisoryXactUnitGrant(t, a, key, advisoryShared, true)
		advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
		advisoryXactUnitGrant(t, a, child, advisoryExclusive, true)
		advisoryXactUnitGrant(t, a, child, advisoryExclusive, true)
		advisoryXactUnitGrant(t, a, child, advisoryShared, false)
		waiter, err := advisoryXactUnitAcquire(t, b, child, advisoryShared, true, false)
		if err != nil || waiter == nil {
			t.Fatal(waiter, err)
		}
		m.Restore(ticket, mark)
		if err := waiter.Wait(); err != nil {
			t.Fatal(err)
		}
		if got := advisoryXactUnitHold(a, key); got.Mode != outer {
			t.Fatal("earlier mode not restored", got, outer)
		}
		if got := advisoryXactUnitHold(a, child); got.Mode != 0 || advisoryUnitHold(a, child).Shared != 1 {
			t.Fatal("child restore crossed lifetime", got)
		}
		m.Restore(ticket, mark)
		if got := advisoryXactUnitHold(a, key); got.Mode != outer {
			t.Fatal("repeat restore changed earlier hold", got)
		}
		m.End(ticket)
		if _, err := advisoryXactUnitAcquire(t, b, key, advisoryExclusive, true, true); err != nil {
			t.Fatal("owner end leaked", err)
		}
	}
	for _, grantBeforeFence := range []bool{false, true} {
		t.Run(fmt.Sprint("fence/", grantBeforeFence), func(t *testing.T) {
			e := New()
			a, ra := advisoryUnitOwner(t, e)
			b, rb := advisoryUnitOwner(t, e)
			key := lockResource{Kind: advisoryPairResource, High: 8, Low: 9}
			advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
			persistent := lockResource{Kind: advisoryBigintResource, Low: 88}
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
				granted := e.locks.entries[key].holders[ticket].Mode == advisoryExclusive
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
			if advisoryUnitHold(a, persistent).Shared != 1 {
				t.Fatal("fence released session hold")
			}
		})
	}
}
