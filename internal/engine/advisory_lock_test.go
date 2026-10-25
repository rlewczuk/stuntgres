package engine

import (
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

func advisoryUnitHold(s *session.Session, key lock.Resource) lock.Hold {
	m := core.EngineOf(s).Locks
	lockState := m.Inspect()
	if entry := lockState.Resources[key]; entry != nil {
		return entry.Holders[s.Advisory.Ticket]
	}
	return lock.Hold{}
}
