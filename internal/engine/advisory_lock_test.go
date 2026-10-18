package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/ids"
)

func advisoryUnitOwner(t *testing.T, e *Engine) (*Session, ids.TxRef) {
	t.Helper()
	s := sessionStart(t, e, nil)
	return s, transactionBegin(t, s)
}
func advisoryUnitAcquire(t *testing.T, s *Session, provenance ids.TxRef, key lockResource, mode relationLockMode, nowait bool) (*relationRequest, error) {
	t.Helper()
	return s.Engine().locks.StartAcquire(context.Background(), s.advisory.ticket, key, mode, relationLockPolicy{Provenance: provenance, NoWait: nowait})
}
func advisoryUnitGrant(t *testing.T, s *Session, provenance ids.TxRef, key lockResource, mode relationLockMode) {
	t.Helper()
	r, err := advisoryUnitAcquire(t, s, provenance, key, mode, true)
	if err != nil || r != nil {
		t.Fatal("immediate grant", r, err)
	}
}
func advisoryUnitHold(s *Session, key lockResource) relationHold {
	m := s.Engine().locks
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry := m.entries[key]; entry != nil {
		return entry.holders[s.advisory.ticket]
	}
	return relationHold{}
}
func advisoryUnitUnlock(t *testing.T, s *Session, key lockResource, mode relationLockMode, want bool) {
	t.Helper()
	got, err := s.Engine().locks.UnlockAdvisory(s.advisory.ticket, key, mode)
	if err != nil || got != want {
		t.Fatal("exact mode unlock", got, err, want)
	}
}
func TestAdvisorySessionKeysCountsCompatibility(t *testing.T) {
	for _, held := range []relationLockMode{advisoryShared, advisoryExclusive} {
		for _, requested := range []relationLockMode{advisoryShared, advisoryExclusive} {
			t.Run(fmt.Sprintf("matrix/%d/%d", held, requested), func(t *testing.T) {
				e := New()
				a, ra := advisoryUnitOwner(t, e)
				b, rb := advisoryUnitOwner(t, e)
				key := lockResource{Kind: advisoryBigintResource, High: 0xffffffff, Low: 0x80000000}
				advisoryUnitGrant(t, a, ra, key, held)
				r, err := advisoryUnitAcquire(t, b, rb, key, requested, true)
				compatible := held == advisoryShared && requested == advisoryShared
				if r != nil || (compatible && err != nil) || (!compatible && !errors.Is(err, errRelationUnavailable)) {
					t.Fatal(r, err, compatible)
				}
				advisoryUnitUnlock(t, a, key, held, true)
				if compatible {
					advisoryUnitUnlock(t, b, key, requested, true)
				} else {
					advisoryUnitGrant(t, b, rb, key, requested)
					advisoryUnitUnlock(t, b, key, requested, true)
				}
			})
		}
	}
	t.Run("disjoint counted grants and overflow", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		big := lockResource{Kind: advisoryBigintResource, High: 0xffffffff, Low: 0xffffffff}
		pair := big
		pair.Kind = advisoryPairResource
		advisoryUnitGrant(t, a, ra, big, advisoryShared)
		advisoryUnitGrant(t, a, ra, big, advisoryShared)
		advisoryUnitGrant(t, a, ra, big, advisoryExclusive)
		advisoryUnitGrant(t, a, ra, big, advisoryExclusive)
		advisoryUnitGrant(t, b, rb, pair, advisoryExclusive)
		want := relationHold{Mode: advisoryShared | advisoryExclusive, Shared: 2, Exclusive: 2}
		if got := advisoryUnitHold(a, big); got != want {
			t.Fatal(got, want)
		}
		advisoryUnitUnlock(t, a, big, advisoryExclusive, true)
		advisoryUnitUnlock(t, a, big, advisoryExclusive, true)
		advisoryUnitUnlock(t, a, big, advisoryExclusive, false)
		if got := advisoryUnitHold(a, big); got.Mode != advisoryShared || got.Shared != 2 || got.Exclusive != 0 {
			t.Fatal(got)
		}
		advisoryUnitGrant(t, b, rb, big, advisoryShared)
		advisoryUnitUnlock(t, b, big, advisoryShared, true)
		m := e.locks
		m.mu.Lock()
		entry := m.entries[big]
		h := entry.holders[a.advisory.ticket]
		h.Shared = ^uint64(0)
		entry.holders[a.advisory.ticket] = h
		m.mu.Unlock()
		r, err := advisoryUnitAcquire(t, a, ra, big, advisoryShared, true)
		if r != nil || err == nil || err.Error() != "stuntgres: advisory hold count exhausted" || advisoryUnitHold(a, big) != h {
			t.Fatal("overflow must not mutate", r, err, advisoryUnitHold(a, big))
		}
		if err = m.UnlockAdvisoryAll(a.advisory.ticket); err != nil {
			t.Fatal(err)
		}
		if advisoryUnitHold(a, big) != (relationHold{}) || advisoryUnitHold(b, pair).Exclusive != 1 {
			t.Fatal("unlock all scope")
		}
		advisoryUnitUnlock(t, b, pair, advisoryExclusive, true)
	})
	t.Run("ordinary checkpoints are independent", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT UNIQUE)")
		ra := transactionBegin(t, a)
		object := catalogRef(t, e, e.CatalogRead(), "t")
		ordinary := lockResource{ObjectRef: object}
		key := lockResource{Kind: advisoryBigintResource, Low: uint32(object.Address.ObjectID)}
		ticket := a.transactions.ticket
		m := e.locks
		if err := m.Acquire(context.Background(), ticket, ordinary, accessShare); err != nil {
			t.Fatal(err)
		}
		checkpoint := m.Checkpoint(ticket)
		if err := m.Acquire(context.Background(), ticket, ordinary, accessExclusive); err != nil {
			t.Fatal(err)
		}
		advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
		advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
		m.Restore(ticket, checkpoint)
		m.mu.Lock()
		ordinaryHold := m.entries[ordinary].holders[ticket]
		persistentJournal := len(a.advisory.ticket.resources)
		m.mu.Unlock()
		if ordinaryHold.Mode != accessShare || ordinaryHold.Shared != 0 || ordinaryHold.Exclusive != 0 || persistentJournal != 1 || advisoryUnitHold(a, key).Exclusive != 2 {
			t.Fatal("checkpoint crossed count domain", ordinaryHold, persistentJournal)
		}
		if err := m.UnlockAdvisoryAll(a.advisory.ticket); err != nil {
			t.Fatal(err)
		}
		m.mu.Lock()
		remaining := m.entries[ordinary].holders[ticket].Mode
		m.mu.Unlock()
		if remaining != accessShare {
			t.Fatal("unlock all removed ordinary mode", remaining)
		}
		transactionEnd(t, a, ra, false)
	})
}
