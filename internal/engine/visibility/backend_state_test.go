package visibility

import (
	"reflect"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestBackendStateSubxactCache(t *testing.T) {
	for _, timestamps := range []bool{false, true} {
		f := newFixture(t)
		reg := NewRegistry(timestamps)
		ref, foreign := f.tx(), f.tx()
		if err := reg.AssignTransactionXIDLocked(ref); err != nil {
			t.Fatal(err)
		}
		top := reg.FullXIDLocked(ref)
		if reg.Entries[ref].SubxactCount != 0 {
			t.Fatal("top entered child cache")
		}
		appendChild := func(parent uint64) uint64 {
			if err := reg.PreflightXIDsLocked(1); err != nil {
				t.Fatal(err)
			}
			return reg.AssignChildXIDLocked(ref, parent)
		}
		outer := appendChild(top)
		var children [64]uint64
		children[0] = outer
		for i := 1; i < 64; i++ {
			children[i] = appendChild(outer)
			reg.ReleaseChildLocked(children[i], outer)
		}
		entry := reg.Entries[ref]
		if entry.SubxactCache != children || entry.SubxactCount != 64 || entry.SubxactOverflowed {
			t.Fatal("exact first64 assignment cache", entry)
		}
		uncached := appendChild(top)
		secondUncached := appendChild(top)
		entry = reg.Entries[ref]
		if entry.SubxactCache != children || entry.SubxactCount != 64 || !entry.SubxactOverflowed {
			t.Fatal("overflow replaced cached identities", entry)
		}
		before := entry
		reg.AbortChildLocked(foreign, outer)
		if reg.Entries[ref] != before || reg.History[outer].State != ids.TxInProgress {
			t.Fatal("foreign authority mutated subtree")
		}
		reg.AbortChildLocked(ref, uncached)
		if reg.Entries[ref] != before {
			t.Fatal("uncached abort changed cache")
		}
		reg.AbortChildLocked(ref, outer)
		entry = reg.Entries[ref]
		if entry.SubxactCount != 0 || !entry.SubxactOverflowed || entry.SubxactCache != [64]uint64{} {
			t.Fatal("released subtree not removed or tail retained", entry)
		}
		if reg.History[secondUncached].State != ids.TxInProgress {
			t.Fatal("uncached survivor aborted")
		}
		newChild := appendChild(top)
		entry = reg.Entries[ref]
		if entry.SubxactCount != 1 || entry.SubxactCache[0] != newChild || !entry.SubxactOverflowed {
			t.Fatal("freed cache did not accept new assignment", entry)
		}
		for _, xid := range entry.SubxactCache[1:] {
			if xid != 0 {
				t.Fatal("uncached survivor refilled cache", xid)
			}
		}
		reg.ResetPhaseLocked(ref)
		if got := reg.Entries[ref]; got.SubxactCache != entry.SubxactCache || got.SubxactCount != 1 || !got.SubxactOverflowed {
			t.Fatal("utility phase erased live cache")
		}
		frontier := reg.LastAssignedXID()
		for range 10 {
			_ = reg.Entries[ref]
		}
		if reg.LastAssignedXID() != frontier {
			t.Fatal("read assigned XID")
		}
		if timestamps {
			reg.RecordCommitTimestampLocked(ref, 12345, 0)
		} else {
			reg.EndTransactionIdentityLocked(ref, false)
		}
		entry = reg.Entries[ref]
		if entry.SubxactCount != 0 || entry.SubxactOverflowed || entry.SubxactCache != [64]uint64{} {
			t.Fatal("top completion retained cache", entry)
		}
		reg.EndTransactionIdentityLocked(ref, false)
		if reg.Entries[ref] != entry {
			t.Fatal("duplicate end changed cleared cache")
		}
	}
	t.Run("full_xid_rollover_and_preflight", func(t *testing.T) {
		f := newFixture(t)
		ref := f.tx()
		reg := NewRegistry(false)
		reg.SetLastAssignedXIDLocked(0xfffffffd)
		if err := reg.AssignTransactionXIDLocked(ref); err != nil {
			t.Fatal(err)
		}
		top := reg.FullXIDLocked(ref)
		first := reg.AssignChildXIDLocked(ref, top)
		second := reg.AssignChildXIDLocked(ref, top)
		if first != 0xffffffff || second != (1<<32|3) {
			t.Fatal("full-XID rollover", first, second)
		}
		reg.AbortChildLocked(ref, first)
		entry := reg.Entries[ref]
		if entry.SubxactCount != 1 || entry.SubxactCache[0] != second || entry.SubxactCache[1] != 0 {
			t.Fatal("full identity compaction", entry)
		}
		reg.SetLastAssignedXIDLocked(^uint64(0) - 1)
		before := reg.Entries[ref]
		history := len(reg.History)
		if err := reg.PreflightXIDsLocked(2); err == nil || !reflect.DeepEqual(before, reg.Entries[ref]) || len(reg.History) != history {
			t.Fatal("failed stack preflight mutated cache", err)
		}
	})
}
