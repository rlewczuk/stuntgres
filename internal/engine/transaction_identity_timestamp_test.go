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
		e.assignedXID = 4294967294
		if err := e.assignTransactionXIDLocked(a.Ref()); err != nil {
			t.Fatal(err)
		}
		if err := e.assignTransactionXIDLocked(b.Ref()); err != nil {
			t.Fatal(err)
		}
		if e.visibility[a.Ref()].fullXID != 4294967295 || e.visibility[b.Ref()].fullXID != 4294967299 || len(e.commitTimestamps) != 2 {
			t.Fatal("sparse wrap reservations")
		}
		if e.commitTimestamps[4294967295].present || e.commitTimestamps[4294967299].present {
			t.Fatal("assignment fabricated completion")
		}
		e.recordCommitTimestampLocked(b.Ref())
		newer := e.lookupCommitTimestampLocked(3)
		e.recordCommitTimestampLocked(a.Ref())
		older := e.lookupCommitTimestampLocked(4294967295)
		if !older.present || !newer.present || e.lastCommittedXID != 4294967295 || len(e.commitTimestamps) != 2 {
			t.Fatal("old-epoch active completion lost sparse slot")
		}
		last := e.lastCommitTimestamp
		e.recordCommitTimestampLocked(a.Ref())
		if e.lastCommitTimestamp != last || e.lookupCommitTimestampLocked(3) != newer {
			t.Fatal("duplicate completion changed history")
		}
	})
	t.Run("timestamp_no_old_epoch_fallback", func(t *testing.T) {
		e := NewWithSettings(nil, "", true)
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		e.mu.Lock()
		e.recordCommitTimestampLocked(a.Ref())
		old := e.lookupCommitTimestampLocked(3)
		e.mu.Unlock()
		if !old.present {
			t.Fatal("missing original actual completion")
		}
		e.endLockOwner(a)
		e.mu.Lock()
		defer e.mu.Unlock()
		e.assignedXID = 4294967295
		if err := e.assignTransactionXIDLocked(b.Ref()); err != nil {
			t.Fatal(err)
		}
		if _, retained := e.commitTimestamps[3]; retained || len(e.commitTimestamps) != 1 {
			t.Fatal("reused low32 retained incompatible timestamp")
		}
		e.endSerializableLocked(b.Ref(), false)
		if e.lookupCommitTimestampLocked(3).present {
			t.Fatal("aborted newest identity fell back to old-epoch completion")
		}
	})
}
