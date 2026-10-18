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
	lockState := m.Inspect()
	if entry := lockState.Resources[key]; entry != nil {
		return entry.Holders[s.advisory.ticket]
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
		lockState := m.Inspect(a.advisory.ticket)
		ordinaryHold := lockState.Resources[ordinary].Holders[ticket]
		persistentJournal := len(lockState.Journals[a.advisory.ticket])
		if ordinaryHold.Mode != accessShare || ordinaryHold.Shared != 0 || ordinaryHold.Exclusive != 0 || persistentJournal != 1 || advisoryUnitHold(a, key).Exclusive != 2 {
			t.Fatal("checkpoint crossed count domain", ordinaryHold, persistentJournal)
		}
		if err := m.UnlockAdvisoryAll(a.advisory.ticket); err != nil {
			t.Fatal(err)
		}
		lockState = m.Inspect()
		remaining := lockState.Resources[ordinary].Holders[ticket].Mode
		if remaining != accessShare {
			t.Fatal("unlock all removed ordinary mode", remaining)
		}
		transactionEnd(t, a, ra, false)
	})
}
