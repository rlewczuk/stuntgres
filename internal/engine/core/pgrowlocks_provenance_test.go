package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/lock"
)

// Native savepoints retain top-level XIDs. This tests effective contribution
// provenance, not PostgreSQL child-XID identity parity. Immutable raw membership
// must survive without resurrecting a rolled-back action on a new successor.
func TestPgrowlocksInheritedOwnerProvenance(t *testing.T) {
	e := engine.New()
	_, a := blockingUnitOwner(t, e)
	_, b := blockingUnitOwner(t, e)
	pgLocksUnitAssign(t, e, a)
	pgLocksUnitAssign(t, e, b)
	tuple := identityMultiUnitTuple(e, 1)
	identityMultiGrant(t, e, a, tuple, lock.ForKeyShare)
	identityMultiEmit(t, e, a, tuple, lock.MultiKeyShare)
	identityMultiGrant(t, e, b, tuple, lock.ForKeyShare)
	identityMultiEmit(t, e, b, tuple, lock.MultiKeyShare)
	saved := e.Locks.Checkpoint(b)
	identityMultiGrant(t, e, b, tuple, lock.ForNoKeyUpdate)
	identityMultiEmit(t, e, b, tuple, lock.MultiNoKeyAction)
	e.Locks.Restore(b, saved)
	raw := e.Locks.Inspect().MultiRows[tuple]
	if raw.ID == 0 || len(raw.Members) != 3 || raw.Members[2].Mode != lock.MultiNoKeyAction {
		t.Fatal("rollback must retain the genuine immutable raw action", raw)
	}
	if rows := pgrowlocksUnitCapture(t, e, b, tuple, false); len(rows) != 1 {
		t.Fatal("rolled-back raw action is not an effective current updater", rows)
	}

	// A later real update copies the original whole raw bag into its successor.
	// B's pre-update contribution has only KEY SHARE. The retained old action
	// must not acquire a new successor contribution merely because owner==B.
	identityMultiGrant(t, e, b, tuple, lock.ForNoKeyUpdate)
	if err := e.Locks.PhysicalEvents(b, []lock.MultiEvent{{Tuple: tuple, Mode: lock.MultiNoKeyAction, Successor: 2}}); err != nil {
		t.Fatal(err)
	}
	successor := tuple
	successor.Version = 2
	inherited := e.Locks.Inspect().MultiRows[successor]
	if inherited.ID != raw.ID || len(inherited.Members) != 3 || inherited.Members[2].Mode != lock.MultiNoKeyAction {
		t.Fatal("successor must retain the complete original raw bag", inherited)
	}
	rows := pgrowlocksUnitCapture(t, e, b, successor, true)
	if len(rows) != 1 || !rows[0].Multi || rows[0].Locker != raw.ID || len(rows[0].Members) != 3 {
		t.Fatal("original live same-owner lock must survive without resurrecting its rolled-back updater", rows)
	}
	bits := e.Locks.Checkpoint(b)[successor.Resource].Multi[successor.Version]
	if bits != 1<<lock.MultiKeyShare {
		t.Fatal("successor copied ineffective original owner modes", bits)
	}
}
