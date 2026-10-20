package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func advisoryXactUnitTicket(s *Session, xact bool) *lock.Ticket {
	if xact {
		return s.transactions.ticket
	}
	return s.advisory.ticket
}
func advisoryXactUnitAcquire(t *testing.T, s *Session, key lock.Resource, mode lock.Mode, xact, nowait bool) (*lock.Request, error) {
	t.Helper()
	return engineOf(s).locks.StartAcquire(context.Background(), advisoryXactUnitTicket(s, xact), key, mode, lock.Policy{Provenance: s.transactions.ref, NoWait: nowait})
}
func advisoryXactUnitGrant(t *testing.T, s *Session, key lock.Resource, mode lock.Mode, xact bool) {
	t.Helper()
	r, err := advisoryXactUnitAcquire(t, s, key, mode, xact, true)
	if r != nil || err != nil {
		t.Fatal("expected immediate grant", r, err)
	}
}
func advisoryXactUnitHold(s *Session, key lock.Resource) lock.Hold {
	m := engineOf(s).locks
	lockState := m.Inspect()
	if e := lockState.Resources[key]; e != nil {
		return e.Holders[s.transactions.ticket]
	}
	return lock.Hold{}
}

func TestAdvisoryXactOwnershipCompatibility(t *testing.T) {
	for _, kind := range []lock.ResourceKind{lock.AdvisoryBigintResource, lock.AdvisoryPairResource} {
		for _, holderXact := range []bool{false, true} {
			for _, requestXact := range []bool{false, true} {
				for _, held := range []lock.Mode{lock.AdvisoryShare, lock.AdvisoryExclusive} {
					for _, requested := range []lock.Mode{lock.AdvisoryShare, lock.AdvisoryExclusive} {
						t.Run(fmt.Sprint(kind, holderXact, requestXact, held, requested), func(t *testing.T) {
							e := New()
							a, _ := advisoryUnitOwner(t, e)
							b, _ := advisoryUnitOwner(t, e)
							key := lock.Resource{Kind: kind, High: 0xffffffff, Low: 0x80000000}
							advisoryXactUnitGrant(t, a, key, held, holderXact)
							r, err := advisoryXactUnitAcquire(t, b, key, requested, requestXact, true)
							compatible := held == lock.AdvisoryShare && requested == lock.AdvisoryShare
							if r != nil || compatible && err != nil || !compatible && !errors.Is(err, lock.ErrUnavailable) {
								t.Fatal(r, err, compatible)
							}
							if !compatible {
								m := e.locks
								lockState := m.Inspect()
								_, journaled := lockState.Journals[advisoryXactUnitTicket(b, requestXact)][key]
								dirty := journaled || len(lockState.Resources[key].Pending) != 0 || lockState.Resources[key].Holders[advisoryXactUnitTicket(b, requestXact)].Mode != 0
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
			key := lock.Resource{Kind: lock.AdvisoryPairResource, High: 7, Low: 8}
			advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, xact)
			r, err := advisoryXactUnitAcquire(t, b, key, lock.AdvisoryExclusive, true, false)
			if err != nil || r == nil {
				t.Fatal(r, err)
			}
			advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, !xact)
			advisoryXactUnitGrant(t, a, key, lock.AdvisoryExclusive, !xact)
			e.endLockOwner(b.transactions.ticket)
			if err := r.Wait(); err == nil {
				t.Fatal("ended waiter granted")
			}
			transactionEnd(t, a, a.transactions.ref, false)
			if err := e.locks.UnlockAdvisoryAll(a.advisory.ticket); err != nil {
				t.Fatal(err)
			}
			transactionBegin(t, a)
			advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, false)
			advisoryXactUnitGrant(t, c, key, lock.AdvisoryShare, true)
			if _, err := advisoryXactUnitAcquire(t, a, key, lock.AdvisoryExclusive, true, true); !errors.Is(err, lock.ErrUnavailable) {
				t.Fatal("ignored external grant", err)
			}
		}
	})
	t.Run("journal overlap and idempotence", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, _ := advisoryUnitOwner(t, e)
		key := lock.Resource{Kind: lock.AdvisoryBigintResource, Low: 32}
		advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, true)
		advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, true)
		if got := advisoryXactUnitHold(a, key); got != (lock.Hold{Mode: lock.AdvisoryShare}) {
			t.Fatal("transaction grant counted", got)
		}
		advisoryXactUnitGrant(t, b, key, lock.AdvisoryShare, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r, err := e.locks.StartAcquire(ctx, a.advisory.ticket, key, lock.AdvisoryExclusive, lock.Policy{Provenance: ra})
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		cancel()
		if err := r.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		lockState := e.locks.Inspect(a.transactions.ticket)
		_, journaled := lockState.Journals[a.transactions.ticket][key]
		if !journaled {
			t.Fatal("session terminal erased transaction hold journal")
		}
		e.endLockOwner(a.transactions.ticket)
		if _, err := advisoryXactUnitAcquire(t, b, key, lock.AdvisoryExclusive, true, true); err != nil {
			t.Fatal("transaction hold leaked", err)
		}
	})
	t.Run("validation and disjoint spaces", func(t *testing.T) {
		e := New()
		a, _ := advisoryUnitOwner(t, e)
		b, _ := advisoryUnitOwner(t, e)
		key := lock.Resource{Kind: lock.AdvisoryBigintResource, High: 1, Low: 2}
		pair := key
		pair.Kind = lock.AdvisoryPairResource
		advisoryXactUnitGrant(t, a, key, lock.AdvisoryExclusive, true)
		advisoryXactUnitGrant(t, b, pair, lock.AdvisoryExclusive, true)
		for _, bad := range []lock.Resource{{Kind: lock.AdvisoryPairResource, Row: 1}, {Kind: lock.AdvisoryBigintResource, ObjectRef: ids.ObjectRef{Address: ids.ObjectAddress{ObjectID: 1}}}} {
			if _, err := advisoryXactUnitAcquire(t, a, bad, lock.AdvisoryExclusive, true, true); err == nil {
				t.Fatal("invalid advisory resource accepted", bad)
			}
		}
		other := New()
		foreign, _ := advisoryUnitOwner(t, other)
		if _, err := e.locks.StartAcquire(context.Background(), foreign.transactions.ticket, key, lock.AdvisoryExclusive, lock.Policy{}); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("foreign owner", err)
		}
		e.locks.DetachPrepared(a.transactions.ticket)
		if _, err := advisoryXactUnitAcquire(t, a, pair, lock.AdvisoryShare, true, true); err == nil {
			t.Fatal("detached requester admitted")
		}
	})
}

func TestAdvisoryXactSavepointLifecycle(t *testing.T) {
	for _, outer := range []lock.Mode{lock.AdvisoryShare, lock.AdvisoryExclusive} {
		e := New()
		a, _ := advisoryUnitOwner(t, e)
		b, _ := advisoryUnitOwner(t, e)
		m := e.locks
		ticket := a.transactions.ticket
		key := lock.Resource{Kind: lock.AdvisoryBigintResource, Low: 1}
		child := lock.Resource{Kind: lock.AdvisoryPairResource, Low: 1}
		advisoryXactUnitGrant(t, a, key, outer, true)
		mark := m.Checkpoint(ticket)
		advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, true)
		advisoryXactUnitGrant(t, a, key, lock.AdvisoryExclusive, true)
		advisoryXactUnitGrant(t, a, child, lock.AdvisoryExclusive, true)
		advisoryXactUnitGrant(t, a, child, lock.AdvisoryExclusive, true)
		advisoryXactUnitGrant(t, a, child, lock.AdvisoryShare, false)
		waiter, err := advisoryXactUnitAcquire(t, b, child, lock.AdvisoryShare, true, false)
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
		if _, err := advisoryXactUnitAcquire(t, b, key, lock.AdvisoryExclusive, true, true); err != nil {
			t.Fatal("owner end leaked", err)
		}
	}
	for _, grantBeforeFence := range []bool{false, true} {
		t.Run(fmt.Sprint("fence/", grantBeforeFence), func(t *testing.T) {
			e := New()
			a, ra := advisoryUnitOwner(t, e)
			b, rb := advisoryUnitOwner(t, e)
			key := lock.Resource{Kind: lock.AdvisoryPairResource, High: 8, Low: 9}
			advisoryXactUnitGrant(t, a, key, lock.AdvisoryExclusive, true)
			persistent := lock.Resource{Kind: lock.AdvisoryBigintResource, Low: 88}
			advisoryUnitGrant(t, a, ra, persistent, lock.AdvisoryShare)
			transactionEnd(t, b, rb, false)
			ticket, _, done := advisoryUnitScopeWait(t, b, key, functions.AdvisoryTransaction)
			if grantBeforeFence {
				e.replacementMu.Lock()
				reserved := true
				t.Cleanup(func() {
					if reserved {
						e.replacementMu.Unlock()
					}
				})
				e.endLockOwner(a.transactions.ticket)
				lockState := e.locks.Inspect()
				granted := lockState.Resources[key].Holders[ticket].Mode == lock.AdvisoryExclusive
				if !granted {
					t.Fatal("no actual pre-fence grant")
				}
				advisoryUnitReplaceReserved(t, e)
				e.replacementMu.Unlock()
				reserved = false
			} else if err := e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
			advisoryUnitScopeResult(t, done, ids.ErrCatalogReplaced)
			c, _ := advisoryUnitOwner(t, e)
			advisoryXactUnitGrant(t, c, key, lock.AdvisoryExclusive, true)
			if advisoryUnitHold(a, persistent).Shared != 1 {
				t.Fatal("fence released session hold")
			}
		})
	}
}
