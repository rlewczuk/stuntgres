package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func advisoryUnitOwner(t *testing.T, e *Engine) (*Session, ids.TxRef) {
	t.Helper()
	s := sessionStart(t, e, nil)
	return s, transactionBegin(t, s)
}
func advisoryUnitAcquire(t *testing.T, s *Session, provenance ids.TxRef, key lock.Resource, mode lock.Mode, nowait bool) (*lock.Request, error) {
	t.Helper()
	return engineOf(s).locks.StartAcquire(context.Background(), s.advisory.ticket, key, mode, lock.Policy{Provenance: provenance, NoWait: nowait})
}
func advisoryUnitGrant(t *testing.T, s *Session, provenance ids.TxRef, key lock.Resource, mode lock.Mode) {
	t.Helper()
	r, err := advisoryUnitAcquire(t, s, provenance, key, mode, true)
	if err != nil || r != nil {
		t.Fatal("immediate grant", r, err)
	}
}
func advisoryUnitHold(s *Session, key lock.Resource) lock.Hold {
	m := engineOf(s).locks
	lockState := m.Inspect()
	if entry := lockState.Resources[key]; entry != nil {
		return entry.Holders[s.advisory.ticket]
	}
	return lock.Hold{}
}
func advisoryUnitUnlock(t *testing.T, s *Session, key lock.Resource, mode lock.Mode, want bool) {
	t.Helper()
	got, err := engineOf(s).locks.UnlockAdvisory(s.advisory.ticket, key, mode)
	if err != nil || got != want {
		t.Fatal("exact mode unlock", got, err, want)
	}
}
func TestAdvisorySessionKeysCountsCompatibility(t *testing.T) {
	for _, held := range []lock.Mode{lock.AdvisoryShare, lock.AdvisoryExclusive} {
		for _, requested := range []lock.Mode{lock.AdvisoryShare, lock.AdvisoryExclusive} {
			t.Run(fmt.Sprintf("matrix/%d/%d", held, requested), func(t *testing.T) {
				e := New()
				a, ra := advisoryUnitOwner(t, e)
				b, rb := advisoryUnitOwner(t, e)
				key := lock.Resource{Kind: lock.AdvisoryBigintResource, High: 0xffffffff, Low: 0x80000000}
				advisoryUnitGrant(t, a, ra, key, held)
				r, err := advisoryUnitAcquire(t, b, rb, key, requested, true)
				compatible := held == lock.AdvisoryShare && requested == lock.AdvisoryShare
				if r != nil || (compatible && err != nil) || (!compatible && !errors.Is(err, lock.ErrUnavailable)) {
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
	t.Run("ordinary checkpoints are independent", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT UNIQUE)")
		ra := transactionBegin(t, a)
		object := catalogRef(t, e, e.CatalogRead(), "t")
		ordinary := lock.Resource{ObjectRef: object}
		key := lock.Resource{Kind: lock.AdvisoryBigintResource, Low: uint32(object.Address.ObjectID)}
		ticket := a.transactions.ticket
		m := e.locks
		if err := m.Acquire(context.Background(), ticket, ordinary, lock.AccessShare); err != nil {
			t.Fatal(err)
		}
		checkpoint := m.Checkpoint(ticket)
		if err := m.Acquire(context.Background(), ticket, ordinary, lock.AccessExclusive); err != nil {
			t.Fatal(err)
		}
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
		m.Restore(ticket, checkpoint)
		lockState := m.Inspect(a.advisory.ticket)
		ordinaryHold := lockState.Resources[ordinary].Holders[ticket]
		persistentJournal := len(lockState.Journals[a.advisory.ticket])
		if ordinaryHold.Mode != lock.AccessShare || ordinaryHold.Shared != 0 || ordinaryHold.Exclusive != 0 || persistentJournal != 1 || advisoryUnitHold(a, key).Exclusive != 2 {
			t.Fatal("checkpoint crossed count domain", ordinaryHold, persistentJournal)
		}
		if err := m.UnlockAdvisoryAll(a.advisory.ticket); err != nil {
			t.Fatal(err)
		}
		lockState = m.Inspect()
		remaining := lockState.Resources[ordinary].Holders[ticket].Mode
		if remaining != lock.AccessShare {
			t.Fatal("unlock all removed ordinary mode", remaining)
		}
		transactionEnd(t, a, ra, false)
	})
}
