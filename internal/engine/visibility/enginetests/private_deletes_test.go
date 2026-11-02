package enginetests

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestPrivateDeletesVersionCollisions(t *testing.T) {
	root := &visibility.PrivateDeletes{}
	first := visibility.DeleteCandidate{Row: 1, Version: 10, Base: 1, RepairStamp: 7, BaseRepairStamp: 3, EndFullXID: 101}
	second := visibility.DeleteCandidate{Row: 1, Version: 20, Base: 2, RepairStamp: 9, BaseRepairStamp: 5, EndFullXID: 102, Moved: true}
	root.Set(first)
	root.Set(second)
	if root.Len() != 2 || len(root.Rows) != 1 || len(root.Versions) != 1 {
		t.Fatal("private sibling ends lack disjoint single authorities")
	}
	for _, tc := range []struct{ selected, target ids.VersionID }{{10, 10}, {20, 20}, {1, 10}, {2, 20}} {
		candidate, ok := root.Lookup(1, tc.selected)
		if !ok || candidate.Version != tc.target || candidate.Row != 1 {
			t.Fatal("selected/base exact lookup lost private lineage", tc, candidate)
		}
	}
	for _, tc := range []struct {
		row     ids.RowID
		version ids.VersionID
	}{{1, 30}, {2, 20}, {1, 0}} {
		if _, ok := root.Lookup(tc.row, tc.version); ok {
			t.Fatal("private deletion fell back to unrelated row/version", tc)
		}
	}
	seen := make(map[ids.VersionID]bool)
	for row, candidate := range root.All() {
		if row != 1 || candidate.Row != row || seen[candidate.Version] {
			t.Fatal("private all iteration duplicated/retargeted candidate")
		}
		seen[candidate.Version] = true
	}
	if len(seen) != 2 {
		t.Fatal("private all iteration omitted collision")
	}
	clone := root.Clone()
	second.EndFullXID = 103
	clone.Set(second)
	if original, _ := root.Lookup(1, 20); original.EndFullXID != 102 || !original.Moved || original.BaseRepairStamp != 5 {
		t.Fatal("clone changed retained collision facts")
	}
	clone.Delete(1, 2)
	if clone.Len() != 2 {
		t.Fatal("exact removal mistakenly consumed a Base match")
	}
	clone.Delete(1, 10)
	if candidate, ok := clone.Lookup(1, 20); !ok || candidate.EndFullXID != 103 || clone.Len() != 1 || root.Len() != 2 {
		t.Fatal("removing primary private candidate erased sibling or old root")
	}
	for _, provenance := range []ids.RowID{0, 2} {
		malformed := &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{1: {Row: provenance, Version: 11}}}
		candidate, ok := malformed.Lookup(1, 11)
		if !ok || candidate.Row != provenance {
			t.Fatal("lookup silently repaired malformed candidate provenance")
		}
		for row, candidate := range malformed.All() {
			if row != 1 || candidate.Row != provenance {
				t.Fatal("iteration hid a malformed candidate from publication validation")
			}
		}
	}
	var absent *visibility.PrivateDeletes
	if absent.Len() != 0 || absent.Clone() != nil {
		t.Fatal("nil private overlay acquired ownership")
	}
	for range absent.All() {
		t.Fatal("nil private overlay enumerated candidate")
	}
}

func TestPrivateDeletesExactRepairStamps(t *testing.T) {
	root := &visibility.PrivateDeletes{}
	root.Set(visibility.DeleteCandidate{Row: 1, Version: 10, Base: 1, RepairStamp: 7, BaseRepairStamp: 2})
	root.Set(visibility.DeleteCandidate{Row: 1, Version: 20, Base: 1, RepairStamp: 9, BaseRepairStamp: 3})
	base := storage.RowVersion{Row: 1, Version: 1, Born: 1, ForcedFrozen: true, RepairStamp: 3}
	if !root.Ends(base) || visibility.LiveVersion(base, 4, nil, root) {
		t.Fatal("matched only stale Base stamp and missed a genuine later sibling end")
	}
	base.RepairStamp = 4
	if root.Ends(base) || !visibility.LiveVersion(base, 4, nil, root) {
		t.Fatal("older private deletion suppressed re-repaired physical version")
	}
	selected := storage.RowVersion{Row: 1, Version: 20, Born: 0, ForcedFrozen: true, RepairStamp: 9}
	if !root.Ends(selected) || visibility.LiveVersion(selected, 4, nil, root) {
		t.Fatal("supplementary exact selected version deletion was ignored")
	}
	selected.RepairStamp = 10
	if root.Ends(selected) || !visibility.LiveVersion(selected, 4, nil, root) {
		t.Fatal("selected version borrowed its predecessor's stamp")
	}
	root.Set(visibility.DeleteCandidate{Row: 1, Version: 1, RepairStamp: 4, EndFullXID: 201})
	if selected, ok := root.Lookup(1, 1); !ok || selected.Version != 1 || selected.RepairStamp != 4 {
		t.Fatal("Base shortcut shadowed a genuine exact selected version")
	}
	if visibility.LiveVersion(base, 4, nil, root) {
		t.Fatal("new exact selected deletion did not apply")
	}
	ends := &storage.DeletionRoot{}
	ends.Set(1, storage.RowEnd{Version: 1, End: 3})
	ends.Set(1, storage.RowEnd{Version: 2, End: 6})
	first := storage.RowVersion{Row: 1, Version: 1, Born: 1}
	second := storage.RowVersion{Row: 1, Version: 2, Born: 2}
	if visibility.LiveVersion(first, 4, ends, nil) || !visibility.LiveVersion(second, 4, ends, nil) || visibility.LiveVersion(second, 6, ends, nil) {
		t.Fatal("ordinary heap liveness collapsed sibling committed ends")
	}
	if visibility.RepeatableConflict(true, 4, second, ends) == nil {
		t.Fatal("repeatable fetch ignored supplementary sibling end")
	}
	var noPrivate *visibility.PrivateDeletes
	normal := storage.RowVersion{Row: 1, Version: 30, Born: 1, ForcedEnd: 3}
	if noPrivate.Ends(normal) || visibility.LiveVersion(normal, 3, nil, noPrivate) || !visibility.LiveVersion(normal, 2, nil, noPrivate) {
		t.Fatal("private predicate borrowed committed/birth semantics or heap ignored exact physical end")
	}
	if visibility.RepeatableConflict(true, 2, normal, nil) == nil {
		t.Fatal("stable fetch ignored normal ancestor's committed physical end")
	}
}
