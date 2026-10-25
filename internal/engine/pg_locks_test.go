package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/lock"
)

func pgLocksUnitCapture(t *testing.T, e *core.Engine) []lock.Record {
	t.Helper()
	records, err := e.Locks.PGLocks(e.Catalog.Domain, e.Catalog.State.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func pgLocksUnitAssign(t *testing.T, e *core.Engine, ticket *lock.Ticket) uint32 {
	t.Helper()
	e.Mu.Lock()
	err := e.AssignTransactionXIDLocked(ticket.Ref())
	xid := e.Visibility.Entries[ticket.Ref()].AssignedXID
	e.Mu.Unlock()
	if err != nil || xid == 0 || ticket.AssignedPublicXID() != xid {
		t.Fatal("canonical assignment mirror", xid, ticket.AssignedPublicXID(), err)
	}
	return xid
}
