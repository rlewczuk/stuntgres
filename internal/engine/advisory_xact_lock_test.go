package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func advisoryXactUnitTicket(s *session.Session, xact bool) *lock.Ticket {
	if xact {
		return s.Transactions.Ticket
	}
	return s.Advisory.Ticket
}

func advisoryXactUnitAcquire(t *testing.T, s *session.Session, key lock.Resource, mode lock.Mode, xact, nowait bool) (*lock.Request, error) {
	t.Helper()
	return core.EngineOf(s).Locks.StartAcquire(context.Background(), advisoryXactUnitTicket(s, xact), key, mode, lock.Policy{Provenance: s.Transactions.Ref, NoWait: nowait})
}

func advisoryXactUnitGrant(t *testing.T, s *session.Session, key lock.Resource, mode lock.Mode, xact bool) {
	t.Helper()
	r, err := advisoryXactUnitAcquire(t, s, key, mode, xact, true)
	if r != nil || err != nil {
		t.Fatal("expected immediate grant", r, err)
	}
}
