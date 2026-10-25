package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func advisoryUnitOwner(t *testing.T, e *core.Engine) (*session.Session, ids.TxRef) {
	t.Helper()
	s := sessionStart(t, e, nil)
	return s, transactionBegin(t, s)
}

func advisoryUnitAcquire(t *testing.T, s *session.Session, provenance ids.TxRef, key lock.Resource, mode lock.Mode, nowait bool) (*lock.Request, error) {
	t.Helper()
	return core.EngineOf(s).Locks.StartAcquire(context.Background(), s.Advisory.Ticket, key, mode, lock.Policy{Provenance: provenance, NoWait: nowait})
}

func advisoryUnitGrant(t *testing.T, s *session.Session, provenance ids.TxRef, key lock.Resource, mode lock.Mode) {
	t.Helper()
	r, err := advisoryUnitAcquire(t, s, provenance, key, mode, true)
	if err != nil || r != nil {
		t.Fatal("immediate grant", r, err)
	}
}

func advisoryUnitHold(s *session.Session, key lock.Resource) lock.Hold {
	m := core.EngineOf(s).Locks
	lockState := m.Inspect()
	if entry := lockState.Resources[key]; entry != nil {
		return entry.Holders[s.Advisory.Ticket]
	}
	return lock.Hold{}
}

func advisoryUnitUnlock(t *testing.T, s *session.Session, key lock.Resource, mode lock.Mode, want bool) {
	t.Helper()
	got, err := core.EngineOf(s).Locks.UnlockAdvisory(s.Advisory.Ticket, key, mode)
	if err != nil || got != want {
		t.Fatal("exact mode unlock", got, err, want)
	}
}
