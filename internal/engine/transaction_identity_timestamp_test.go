package engine

import "testing"

func identityUnitTimestampWrap(t *testing.T) {
	t.Helper()
	t.Run("timestamp_out_of_order_wrap", func(t *testing.T) {
		e := NewWithSettings(nil, "", true)
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		e.Mu.Lock()
		defer e.Mu.Unlock()
		e.Visibility.SetLastAssignedXIDLocked(4294967294)
		if err := e.AssignTransactionXIDLocked(a.Ref()); err != nil {
			t.Fatal(err)
		}
		if err := e.AssignTransactionXIDLocked(b.Ref()); err != nil {
			t.Fatal(err)
		}
		if e.Visibility.Entries[a.Ref()].FullXID != 4294967295 || e.Visibility.Entries[b.Ref()].FullXID != 4294967299 || len(e.Visibility.CommitTimestamps) != 2 {
			t.Fatal("sparse wrap reservations")
		}
		if e.Visibility.CommitTimestamps[4294967295].Present || e.Visibility.CommitTimestamps[4294967299].Present {
			t.Fatal("assignment fabricated completion")
		}
		e.Visibility.RecordCommitTimestampLocked(b.Ref())
		newer := e.Visibility.LookupCommitTimestampLocked(3)
		e.Visibility.RecordCommitTimestampLocked(a.Ref())
		older := e.Visibility.LookupCommitTimestampLocked(4294967295)
		if !older.Present || !newer.Present || e.Visibility.LastCommittedXID() != 4294967295 || len(e.Visibility.CommitTimestamps) != 2 {
			t.Fatal("old-epoch active completion lost sparse slot")
		}
		last := e.Visibility.LastCommitTimestamp()
		e.Visibility.RecordCommitTimestampLocked(a.Ref())
		if e.Visibility.LastCommitTimestamp() != last || e.Visibility.LookupCommitTimestampLocked(3) != newer {
			t.Fatal("duplicate completion changed history")
		}
	})
	t.Run("timestamp_no_old_epoch_fallback", func(t *testing.T) {
		e := NewWithSettings(nil, "", true)
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		e.Mu.Lock()
		e.Visibility.RecordCommitTimestampLocked(a.Ref())
		old := e.Visibility.LookupCommitTimestampLocked(3)
		e.Mu.Unlock()
		if !old.Present {
			t.Fatal("missing original actual completion")
		}
		e.EndLockOwner(a)
		e.Mu.Lock()
		defer e.Mu.Unlock()
		e.Visibility.SetLastAssignedXIDLocked(4294967295)
		if err := e.AssignTransactionXIDLocked(b.Ref()); err != nil {
			t.Fatal(err)
		}
		if _, retained := e.Visibility.CommitTimestamps[3]; retained || len(e.Visibility.CommitTimestamps) != 1 {
			t.Fatal("reused low32 retained incompatible timestamp")
		}
		EngineEndSerializableLocked(e, b.Ref(), false)
		if e.Visibility.LookupCommitTimestampLocked(3).Present {
			t.Fatal("aborted newest identity fell back to old-epoch completion")
		}
	})
}
