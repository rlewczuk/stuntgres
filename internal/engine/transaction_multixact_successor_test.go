package engine

import (
	"errors"
	"testing"
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
		identityMultiGrant(t, e, a, original, keyShare)
		identityMultiEmit(t, e, a, original, multiKeyShare)
		transactionEnd(t, as, a.ref, false)
		identityMultiGrant(t, e, c, original, noKeyUpdate)
		identityMultiEmit(t, e, c, original, multiNoKeyAction)
		if err := e.locks.physicalEvents(c, []multiEvent{{tuple: original, mode: multiNoKeyAction, successor: 2}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.version = 2
		if _, ok := e.locks.multiRows[successor]; ok || e.locks.nextMultiXact() != 1 {
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
		identityMultiGrant(t, e, a, original, keyShare)
		identityMultiEmit(t, e, a, original, multiKeyShare)
		identityMultiGrant(t, e, b, original, keyShare)
		identityMultiEmit(t, e, b, original, multiKeyShare)
		transactionEnd(t, bs, b.ref, false)
		identityMultiGrant(t, e, c, original, noKeyUpdate)
		identityMultiEmit(t, e, c, original, multiNoKeyAction)
		if err := e.locks.physicalEvents(c, []multiEvent{{tuple: original, mode: multiNoKeyAction, successor: 2}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.version = 2
		if e.locks.multiRows[successor].id != 1 || len(e.locks.multiRows[successor].members) != 2 || e.locks.nextMultiXact() != 3 {
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
		identityMultiGrant(t, e, a, original, keyShare)
		identityMultiEmit(t, e, a, original, multiKeyShare)
		identityMultiGrant(t, e, b, original, keyShare)
		identityMultiEmit(t, e, b, original, multiKeyShare)
		identityMultiGrant(t, e, c, original, noKeyUpdate)
		identityMultiEmit(t, e, c, original, multiNoKeyAction)
		if err := e.locks.physicalEvents(c, []multiEvent{{tuple: original, mode: multiNoKeyAction, successor: 3}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.version = 3
		if e.locks.nextMultiXact() != 3 || e.locks.multiRows[successor].id != 1 {
			t.Fatal("successor fabricated an ID or lost original raw K")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/keysh")
		identityMultiWant(t, e, 2, "3/keysh", "4/keysh", "5/nokeyupd")
		transactionEnd(t, cs, c.ref, true)
		transactionEnd(t, bs, b.ref, false)
		// K still includes ended B. Live A selects multi expansion, not singleton
		// collapse; the successor never follows the committed C action record.
		identityMultiGrant(t, e, a, successor, rowUpdate)
		identityMultiEmit(t, e, a, successor, multiUpdateLock)
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
		identityMultiGrant(t, e, a, original, keyShare)
		identityMultiEmit(t, e, a, original, multiKeyShare)
		identityMultiGrant(t, e, c, original, noKeyUpdate)
		identityMultiEmit(t, e, c, original, multiNoKeyLock)
		identityMultiEmit(t, e, c, original, multiNoKeyAction)
		if err := e.locks.physicalEvents(c, []multiEvent{{tuple: original, mode: multiNoKeyAction, successor: 2}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.version = 2
		if e.locks.multiRows[successor].id != 1 {
			t.Fatal("mixed raw K not inherited")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
		identityMultiWant(t, e, 2, "3/keysh", "4/fornokeyupd", "4/nokeyupd")
		identityMultiGrant(t, e, c, successor, rowShare)
		identityMultiEmit(t, e, c, successor, multiShare)
		if e.locks.nextMultiXact() != 3 || e.locks.multiRows[successor].id != 1 {
			t.Fatal("strong own member appended a weaker status")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
	})
	t.Run("same_xid_single_strength_preserves_action_kind", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		original := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, original, rowUpdate)
		identityMultiEmit(t, e, a, original, multiUpdateLock)
		identityMultiEmit(t, e, a, original, multiNoKeyAction)
		row := e.locks.multiRows[original]
		if row.id != 0 || len(row.members) != 1 || row.members[0].mode != multiUpdateAction || e.locks.nextMultiXact() != 1 {
			t.Fatal("single same-XID strength merge confused lock versus action")
		}
		if err := e.locks.physicalEvents(a, []multiEvent{{tuple: original, mode: multiNoKeyAction, successor: 2}}); err != nil {
			t.Fatal(err)
		}
		successor := original
		successor.version = 2
		inherited := e.locks.multiRows[successor]
		if inherited.id != 0 || len(inherited.members) != 1 || inherited.members[0].mode != multiUpdateLock {
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
		identityMultiGrant(t, e, a, first, keyShare)
		identityMultiEmit(t, e, a, first, multiKeyShare)
		identityMultiGrant(t, e, b, second, keyShare)
		identityMultiEmit(t, e, b, second, multiKeyShare)
		identityMultiGrant(t, e, b, first, keyShare)
		identityMultiEmit(t, e, b, first, multiKeyShare)
		identityMultiGrant(t, e, a, second, keyShare)
		identityMultiEmit(t, e, a, second, multiKeyShare)
		if e.locks.nextMultiXact() != 3 {
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
		identityMultiGrant(t, e, a, first, keyShare)
		identityMultiEmit(t, e, a, first, multiKeyShare)
		identityMultiGrant(t, e, b, first, keyShare)
		identityMultiEmit(t, e, b, first, multiKeyShare)
		counter := e.assignedXID
		if _, err := e.locks.multiXactMembers(reader, 1); err != nil {
			t.Fatal(err)
		}
		if reader.assignedPublicXID() != 0 || e.assignedXID != counter {
			t.Fatal("member inquiry assigned an observer XID")
		}
		if _, err := e.locks.multiXactMembers(a, 1); err != nil {
			t.Fatal(err)
		}
		identityMultiGrant(t, e, b, second, keyShare)
		identityMultiEmit(t, e, b, second, multiKeyShare)
		identityMultiGrant(t, e, a, second, keyShare)
		identityMultiEmit(t, e, a, second, multiKeyShare)
		if e.locks.nextMultiXact() != 2 || e.locks.multiRows[second].id != 1 {
			t.Fatal("inquiry did not populate the actual caller cache")
		}
		e.endLockOwner(reader)
		if _, err := e.locks.multiXactMembers(reader, 1); !errors.Is(err, ErrCatalogReplaced) {
			t.Fatal("retired owner resurrected read/cache authority", err)
		}
		if _, err := e.locks.multiAge(reader, 1); !errors.Is(err, ErrCatalogReplaced) {
			t.Fatal("retired owner resurrected counter authority", err)
		}
	})
	t.Run("transaction_cache_lru_256", func(t *testing.T) {
		var cache []uint32
		for id := uint32(1); id <= 257; id++ {
			cache = rememberMultiXact(cache, id)
		}
		if len(cache) != 256 || cache[0] != 257 || cache[255] != 2 {
			t.Fatal("cache cap/oldest eviction", len(cache), cache[0], cache[len(cache)-1])
		}
		cache = rememberMultiXact(cache, 2)
		cache = rememberMultiXact(cache, 258)
		if len(cache) != 256 || cache[0] != 258 || cache[1] != 2 || cache[255] != 4 {
			t.Fatal("cache hit did not refresh LRU")
		}
		if got := testing.AllocsPerRun(100, func() { cache = rememberMultiXact(cache, 2) }); got != 0 {
			t.Fatal("cache reentry allocated", got)
		}
	})
}
