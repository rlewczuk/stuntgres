package engine

import "testing"

func identityUnitTimestampWrap(t *testing.T) {
	t.Helper()
	t.Run("timestamp_out_of_order_wrap", func(t *testing.T) {
		e := NewWithSettings(nil, "", true)
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		e.mu.Lock()
		defer e.mu.Unlock()
		e.visibility.assignedXID = 4294967294
		if err := e.assignTransactionXIDLocked(a.Ref()); err != nil {
			t.Fatal(err)
		}
		if err := e.assignTransactionXIDLocked(b.Ref()); err != nil {
			t.Fatal(err)
		}
		if e.visibility.entries[a.Ref()].fullXID != 4294967295 || e.visibility.entries[b.Ref()].fullXID != 4294967299 || len(e.visibility.commitTimestamps) != 2 {
			t.Fatal("sparse wrap reservations")
		}
		if e.visibility.commitTimestamps[4294967295].present || e.visibility.commitTimestamps[4294967299].present {
			t.Fatal("assignment fabricated completion")
		}
		e.visibility.recordCommitTimestampLocked(b.Ref())
		newer := e.visibility.lookupCommitTimestampLocked(3)
		e.visibility.recordCommitTimestampLocked(a.Ref())
		older := e.visibility.lookupCommitTimestampLocked(4294967295)
		if !older.present || !newer.present || e.visibility.lastCommittedXID != 4294967295 || len(e.visibility.commitTimestamps) != 2 {
			t.Fatal("old-epoch active completion lost sparse slot")
		}
		last := e.visibility.lastCommitTimestamp
		e.visibility.recordCommitTimestampLocked(a.Ref())
		if e.visibility.lastCommitTimestamp != last || e.visibility.lookupCommitTimestampLocked(3) != newer {
			t.Fatal("duplicate completion changed history")
		}
	})
	t.Run("timestamp_no_old_epoch_fallback", func(t *testing.T) {
		e := NewWithSettings(nil, "", true)
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		e.mu.Lock()
		e.visibility.recordCommitTimestampLocked(a.Ref())
		old := e.visibility.lookupCommitTimestampLocked(3)
		e.mu.Unlock()
		if !old.present {
			t.Fatal("missing original actual completion")
		}
		e.endLockOwner(a)
		e.mu.Lock()
		defer e.mu.Unlock()
		e.visibility.assignedXID = 4294967295
		if err := e.assignTransactionXIDLocked(b.Ref()); err != nil {
			t.Fatal(err)
		}
		if _, retained := e.visibility.commitTimestamps[3]; retained || len(e.visibility.commitTimestamps) != 1 {
			t.Fatal("reused low32 retained incompatible timestamp")
		}
		e.endSerializableLocked(b.Ref(), false)
		if e.visibility.lookupCommitTimestampLocked(3).present {
			t.Fatal("aborted newest identity fell back to old-epoch completion")
		}
	})
}
