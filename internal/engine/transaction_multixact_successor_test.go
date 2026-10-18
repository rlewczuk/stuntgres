package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func identityMultiSuccessorUnits(t *testing.T) {
	t.Helper()
	t.Run("no_surviving_original_locker_invalidates_successor", func(t *testing.T) {
		e := New()
		as, a := blockingUnitOwner(t, e)
		_, c := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, c)
		original := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, original, lock.ForKeyShare)
		identityMultiEmit(t, e, a, original, lock.MultiKeyShare)
		transactionEnd(t, as, a.Ref(), false)
		identityMultiGrant(t, e, c, original, lock.ForNoKeyUpdate)
		identityMultiEmit(t, e, c, original, lock.MultiNoKeyAction)
		if err := e.locks.PhysicalEvents(c, []lock.MultiEvent{{Tuple: original, Mode: lock.MultiNoKeyAction, Successor: 2}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.Version = 2
		if _, ok := e.locks.Inspect().MultiRows[successor]; ok || e.locks.NextMultiXact() != 1 {
			t.Fatal("ended single locker or fresh updater leaked into successor")
		}
	})
	t.Run("ended_lock_member_copied_in_original_raw_bag", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		bs, b := blockingUnitOwner(t, e)
		_, c := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, b)
		pgLocksUnitAssign(t, e, c)
		original := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, original, lock.ForKeyShare)
		identityMultiEmit(t, e, a, original, lock.MultiKeyShare)
		identityMultiGrant(t, e, b, original, lock.ForKeyShare)
		identityMultiEmit(t, e, b, original, lock.MultiKeyShare)
		transactionEnd(t, bs, b.Ref(), false)
		identityMultiGrant(t, e, c, original, lock.ForNoKeyUpdate)
		identityMultiEmit(t, e, c, original, lock.MultiNoKeyAction)
		if err := e.locks.PhysicalEvents(c, []lock.MultiEvent{{Tuple: original, Mode: lock.MultiNoKeyAction, Successor: 2}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.Version = 2
		if e.locks.Inspect().MultiRows[successor].ID != 1 || len(e.locks.Inspect().MultiRows[successor].Members) != 2 || e.locks.NextMultiXact() != 3 {
			t.Fatal("ended member was pruned while copying original raw K")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/keysh")
		identityMultiWant(t, e, 2, "3/keysh", "5/nokeyupd")
	})
	t.Run("original_raw_successor_identity", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		bs, b := blockingUnitOwner(t, e)
		cs, c := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, b)
		pgLocksUnitAssign(t, e, c)
		original := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, original, lock.ForKeyShare)
		identityMultiEmit(t, e, a, original, lock.MultiKeyShare)
		identityMultiGrant(t, e, b, original, lock.ForKeyShare)
		identityMultiEmit(t, e, b, original, lock.MultiKeyShare)
		identityMultiGrant(t, e, c, original, lock.ForNoKeyUpdate)
		identityMultiEmit(t, e, c, original, lock.MultiNoKeyAction)
		if err := e.locks.PhysicalEvents(c, []lock.MultiEvent{{Tuple: original, Mode: lock.MultiNoKeyAction, Successor: 3}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.Version = 3
		if e.locks.NextMultiXact() != 3 || e.locks.Inspect().MultiRows[successor].ID != 1 {
			t.Fatal("successor fabricated an ID or lost original raw K")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/keysh")
		identityMultiWant(t, e, 2, "3/keysh", "4/keysh", "5/nokeyupd")
		transactionEnd(t, cs, c.Ref(), true)
		transactionEnd(t, bs, b.Ref(), false)
		// K still includes ended B. Live A selects multi expansion, not singleton
		// collapse; the successor never follows the committed C action record.
		identityMultiGrant(t, e, a, successor, lock.ForUpdate)
		identityMultiEmit(t, e, a, successor, lock.MultiUpdateLock)
		identityMultiWant(t, e, 3, "3/keysh", "3/forupd")
		identityMultiWant(t, e, 1, "3/keysh", "4/keysh")
		identityMultiWant(t, e, 2, "3/keysh", "4/keysh", "5/nokeyupd")
	})
	t.Run("whole_mixed_lock_successor_and_weaker_reentry", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		_, c := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, c)
		original := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, original, lock.ForKeyShare)
		identityMultiEmit(t, e, a, original, lock.MultiKeyShare)
		identityMultiGrant(t, e, c, original, lock.ForNoKeyUpdate)
		identityMultiEmit(t, e, c, original, lock.MultiNoKeyLock)
		identityMultiEmit(t, e, c, original, lock.MultiNoKeyAction)
		if err := e.locks.PhysicalEvents(c, []lock.MultiEvent{{Tuple: original, Mode: lock.MultiNoKeyAction, Successor: 2}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.Version = 2
		if e.locks.Inspect().MultiRows[successor].ID != 1 {
			t.Fatal("mixed raw K not inherited")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
		identityMultiWant(t, e, 2, "3/keysh", "4/fornokeyupd", "4/nokeyupd")
		identityMultiGrant(t, e, c, successor, lock.ForShare)
		identityMultiEmit(t, e, c, successor, lock.MultiShare)
		if e.locks.NextMultiXact() != 3 || e.locks.Inspect().MultiRows[successor].ID != 1 {
			t.Fatal("strong own member appended a weaker status")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
	})
	t.Run("same_xid_single_strength_preserves_action_kind", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		original := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, original, lock.ForUpdate)
		identityMultiEmit(t, e, a, original, lock.MultiUpdateLock)
		identityMultiEmit(t, e, a, original, lock.MultiNoKeyAction)
		row := e.locks.Inspect().MultiRows[original]
		if row.ID != 0 || len(row.Members) != 1 || row.Members[0].Mode != lock.MultiUpdateAction || e.locks.NextMultiXact() != 1 {
			t.Fatal("single same-XID strength merge confused lock versus action")
		}
		if err := e.locks.PhysicalEvents(a, []lock.MultiEvent{{Tuple: original, Mode: lock.MultiNoKeyAction, Successor: 2}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.Version = 2
		inherited := e.locks.Inspect().MultiRows[successor]
		if inherited.ID != 0 || len(inherited.Members) != 1 || inherited.Members[0].Mode != lock.MultiUpdateLock {
			t.Fatal("single successor inherited action instead of original lock")
		}
	})
}

func identityMultiCacheUnits(t *testing.T) {
	t.Helper()
	t.Run("cache_is_not_global", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, b)
		first, second := identityMultiUnitTuple(e, 1), identityMultiUnitTuple(e, 2)
		identityMultiGrant(t, e, a, first, lock.ForKeyShare)
		identityMultiEmit(t, e, a, first, lock.MultiKeyShare)
		identityMultiGrant(t, e, b, second, lock.ForKeyShare)
		identityMultiEmit(t, e, b, second, lock.MultiKeyShare)
		identityMultiGrant(t, e, b, first, lock.ForKeyShare)
		identityMultiEmit(t, e, b, first, lock.MultiKeyShare)
		identityMultiGrant(t, e, a, second, lock.ForKeyShare)
		identityMultiEmit(t, e, a, second, lock.MultiKeyShare)
		if e.locks.NextMultiXact() != 3 {
			t.Fatal("different backend incorrectly used universal member-set interning")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/keysh")
		identityMultiWant(t, e, 2, "3/keysh", "4/keysh")
	})
	t.Run("read_populates_only_current_transaction_cache", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		_, reader := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, b)
		first, second := identityMultiUnitTuple(e, 1), identityMultiUnitTuple(e, 2)
		identityMultiGrant(t, e, a, first, lock.ForKeyShare)
		identityMultiEmit(t, e, a, first, lock.MultiKeyShare)
		identityMultiGrant(t, e, b, first, lock.ForKeyShare)
		identityMultiEmit(t, e, b, first, lock.MultiKeyShare)
		counter := e.assignedXID
		if _, err := e.locks.MultiXactMembers(reader, 1); err != nil {
			t.Fatal(err)
		}
		if reader.AssignedPublicXID() != 0 || e.assignedXID != counter {
			t.Fatal("member inquiry assigned an observer XID")
		}
		if _, err := e.locks.MultiXactMembers(a, 1); err != nil {
			t.Fatal(err)
		}
		identityMultiGrant(t, e, b, second, lock.ForKeyShare)
		identityMultiEmit(t, e, b, second, lock.MultiKeyShare)
		identityMultiGrant(t, e, a, second, lock.ForKeyShare)
		identityMultiEmit(t, e, a, second, lock.MultiKeyShare)
		if e.locks.NextMultiXact() != 2 || e.locks.Inspect().MultiRows[second].ID != 1 {
			t.Fatal("inquiry did not populate the actual caller cache")
		}
		e.endLockOwner(reader)
		if _, err := e.locks.MultiXactMembers(reader, 1); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("retired owner resurrected read/cache authority", err)
		}
		if _, err := e.locks.MultiAge(reader, 1); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("retired owner resurrected counter authority", err)
		}
	})
}
