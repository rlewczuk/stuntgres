package enginetests

import (
	"context"
	"math"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestPgsurgeryFreezeStorage(t *testing.T) {
	payload := make([]sqltype.Value, 1)
	old := storage.RowVersion{Row: 7, Version: 11, Born: 1, InsertFullXID: 31, DeleteFullXID: 32, Values: payload}
	head := storage.RowVersion{Row: 7, Version: 12, Born: 2, InsertFullXID: 32, Values: payload}
	root, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{old, old})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Additional) != 1 || root.Versions.Len() != 1 {
		t.Fatal("duplicate physical repair", root)
	}
	frozen := root.Apply(old)
	if frozen.Row != old.Row || frozen.Version != old.Version || frozen.HeapXmin() != 2 || frozen.HeapXmax() != 0 || &frozen.Values[0] != &payload[0] {
		t.Fatal("changed version/payload instead of header", frozen)
	}
	untouched := root.Apply(head)
	if untouched.Row != head.Row || untouched.Version != head.Version || untouched.HeapXmin() != 32 || untouched.HeapXmax() != 0 || &untouched.Values[0] != &payload[0] {
		t.Fatal("unselected successor header changed")
	}
	if old.ForcedFrozen || old.InsertFullXID != 31 || old.DeleteFullXID != 32 {
		t.Fatal("captured old header mutated")
	}
	if storage.EndHeapHeaders(root, []storage.RowVersion{old}, 40) != root {
		t.Fatal("pre-repair writer restored cleared xmax")
	}
	future := frozen
	future.RepairStamp++
	if storage.EndHeapHeaders(root, []storage.RowVersion{future}, 40) != root {
		t.Fatal("impossible future admission accepted")
	}
	ended := storage.CommitHeapEnds(root, []storage.RowVersion{frozen}, 40, 4)
	if ended == root || ended.Apply(old).HeapXmax() != 40 || ended.Apply(old).ForcedEnd != 4 || root.Apply(old).ForcedEnd != 0 {
		t.Fatal("immutable exact committed end", ended, root)
	}
	again, err := storage.RepairHeapHeaders(ended, []storage.RowVersion{old})
	if err != nil || again.Apply(old).HeapXmax() != 0 || again.Apply(old).ForcedEnd != 0 || again.Apply(old).RepairStamp <= frozen.RepairStamp {
		t.Fatal("second repair failed to clear actual later end", again, err)
	}
	if ended.Apply(old).ForcedEnd != 4 {
		t.Fatal("second repair changed retained header capture")
	}
	exhausted := &storage.HeapHeaderRoot{Stamp: math.MaxUint64}
	if candidate, err := storage.RepairHeapHeaders(exhausted, []storage.RowVersion{old}); err == nil || candidate != nil || exhausted.Stamp != math.MaxUint64 {
		t.Fatal("repair identity wrapped or partially published", candidate, err)
	}

	source := &exec.Source{Committed: []storage.RowVersion{head}, Headers: root, Repaired: root.Additional}
	selected, err := exec.SelectQuerySource(context.Background(), source, 2, nil, nil, visibility.HeapOperations{})
	if err != nil || selected.Count() != 2 {
		t.Fatal("same-RowID physical candidates collapsed", selected, err)
	}
	versions := map[ids.VersionID]storage.RowVersion{}
	for i := range selected.Count() {
		row := selected.VersionAt(selected.RawIndex(i))
		versions[row.Version] = row
	}
	if len(versions) != 2 || versions[11].HeapXmin() != 2 || versions[12].HeapXmin() != 32 {
		t.Fatal("selection retargeted a logical head", versions)
	}
	// A repaired private tuple later appears in its owner's committed vector:
	// the immutable additional capture must not yield the same VersionID twice.
	source = &exec.Source{Committed: []storage.RowVersion{old, head}, Headers: root, Repaired: root.Additional}
	selected, err = exec.SelectQuerySource(context.Background(), source, 2, nil, nil, visibility.HeapOperations{})
	if err != nil || selected.Count() != 2 {
		t.Fatal("committed repair duplicated", selected, err)
	}
	store := &storage.Relation{Versions: []storage.RowVersion{head}, Headers: root, Retained: &storage.HeapPredecessor{Version: old}}
	if row, _, found := store.PhysicalVersion(11, -1); !found || row.Version != 11 || row.HeapXmin() != 2 {
		t.Fatal("exact predecessor lookup", row, found)
	}
	if _, _, found := store.PhysicalVersion(999, -1); found {
		t.Fatal("nonexistent version invented")
	}

	heads := []storage.RowVersion{head}
	allocations := testing.AllocsPerRun(100, func() {
		s := exec.Source{Committed: heads}
		if _, err := exec.SelectQuerySource(context.Background(), &s, 2, nil, nil, visibility.HeapOperations{}); err != nil {
			t.Fatal(err)
		}
	})
	if allocations != 0 {
		t.Fatal("ordinary no-repair selection allocated", allocations)
	}
}

func TestPgsurgeryFreezePhysicalVersionHints(t *testing.T) {
	payload := make([]sqltype.Value, 1)
	other := storage.RowVersion{Row: 8, Version: 8, Born: 1, InsertFullXID: 28, Values: payload}
	sibling := storage.RowVersion{Row: 7, Version: 12, Born: 2, InsertFullXID: 32, Values: payload}
	current := storage.RowVersion{Row: 7, Version: 14, Born: 3, InsertFullXID: 34, DeleteFullXID: 35, Values: payload}
	retained := storage.RowVersion{Row: 7, Version: 11, Born: 1, InsertFullXID: 31, Values: payload}
	discarded := storage.RowVersion{Row: 7, Version: 13, InsertFullXID: 33, Values: payload}
	additional := storage.RowVersion{Row: 7, Version: 15, InsertFullXID: 35, Values: payload}
	store := &storage.Relation{
		Versions:  []storage.RowVersion{other, sibling, current},
		Retained:  &storage.HeapPredecessor{Version: retained},
		Discarded: &storage.HeapPredecessor{Version: discarded},
	}
	capture := *store
	headers, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{current, additional})
	if err != nil {
		t.Fatal(err)
	}
	store.Headers = headers
	for _, tc := range []struct {
		name string
		want storage.RowVersion
		hint int
		slot int
		xmin uint32
	}{
		{"known", current, 2, 2, 2},
		{"stale_same_row", current, 1, 2, 2},
		{"stale_other_row", current, 0, 2, 2},
		{"unknown", current, -1, 2, 2},
		{"out_of_bounds", current, len(store.Versions), 2, 2},
		{"sibling", sibling, 2, 1, 32},
		{"retained", retained, 2, -1, 31},
		{"discarded", discarded, 2, -1, 33},
		{"additional", additional, 2, -1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, slot, found := store.PhysicalVersion(tc.want.Version, tc.hint)
			if !found || got.Row != tc.want.Row || got.Version != tc.want.Version || slot != tc.slot || got.HeapXmin() != tc.xmin || got.HeapXmax() != 0 || &got.Values[0] != &tc.want.Values[0] {
				t.Fatal("hint changed exact physical identity, header or payload backing", got, slot, found)
			}
		})
	}
	for _, version := range []ids.VersionID{0, 999} {
		if _, _, found := store.PhysicalVersion(version, 2); found {
			t.Fatal("hint invented a physical version", version)
		}
	}
	old, slot, found := capture.PhysicalVersion(current.Version, 2)
	if !found || slot != 2 || old.HeapXmin() != 34 || old.HeapXmax() != 35 || old.ForcedFrozen || capture.Headers != nil || store.Versions[2].ForcedFrozen || &old.Values[0] != &current.Values[0] {
		t.Fatal("current lookup mutated an old capture or canonical version")
	}
	for _, relation := range []*storage.Relation{&capture, store} {
		allocations := testing.AllocsPerRun(100, func() {
			got, slot, found := relation.PhysicalVersion(current.Version, 2)
			if !found || slot != 2 || got.Version != current.Version {
				t.Fatal("known exact lookup failed")
			}
		})
		if allocations != 0 {
			t.Fatal("known physical lookup allocated", allocations)
		}
	}
}

func TestPgsurgeryFreezeDiscardedRoots(t *testing.T) {
	payload := make([]sqltype.Value, 1)
	committed := storage.RowVersion{Row: 1, Version: 1, Born: 1, InsertFullXID: 30, Values: payload}
	kept := storage.RowVersion{Row: 1, Version: 2, InsertFullXID: 31, Values: payload, PrivateHistory: storage.RetainHeapPredecessor(committed)}
	discarded := storage.RowVersion{Row: 1, Version: 3, InsertFullXID: 31, Values: payload, PrivateHistory: storage.RetainHeapPredecessor(kept)}
	root, changed := storage.RetainDiscardedVersions(nil, []storage.RowVersion{discarded}, []storage.RowVersion{kept})
	if !changed || root == nil || root.Version.Version != 3 || root.Next != nil || root.Version.PrivateHistory != nil || &root.Version.Values[0] != &payload[0] {
		t.Fatal("discard transfer lost actual identities/backing", root)
	}
	if discarded.PrivateHistory == nil || discarded.PrivateHistory.Version.Version != 2 {
		t.Fatal("owner history mutated")
	}
	if same, changed := storage.RetainDiscardedVersions(root, []storage.RowVersion{discarded}, []storage.RowVersion{kept}); changed || same != root {
		t.Fatal("discard duplicate created", same)
	}
	all, changed := storage.RetainDiscardedVersions(nil, []storage.RowVersion{discarded}, nil)
	if !changed || all == nil || all.Next == nil || all.Next.Next != nil || all.Next.Version.Born != 0 {
		t.Fatal("actual private history missing or committed base discarded", all)
	}
	if merged, changed := storage.MergeDiscardedVersions(nil, all, nil); !changed || merged != all {
		t.Fatal("unnecessary deferred-batch copy")
	}
	if same, changed := storage.MergeDiscardedVersions(root, all, []storage.RowVersion{kept}); changed || same != root {
		t.Fatal("surviving savepoint version reclassified aborted")
	}
	if root, changed := storage.RetainDiscardedVersions(nil, []storage.RowVersion{{Row: 2, Values: payload}}, nil); changed || root != nil {
		t.Fatal("uncreated bound input became a physical tuple")
	}
}

func TestPgsurgeryFreezeHeaderChains(t *testing.T) {
	a := storage.RowVersion{Row: 7, Version: 10, Born: 1, InsertFullXID: 30}
	b := storage.RowVersion{Row: 7, Version: 20, Base: a.Version, InsertFullXID: 40, PrivateHistory: storage.RetainHeapPredecessor(a)}
	rawA := storage.ReplaceHeapHeaders(nil, []storage.RowVersion{b}, 40)
	if rawA.Header(a.Version).Successor != b.Version || rawA.Header(a.Version).Xmax != 40 {
		t.Fatal("missing actual first UPDATE header")
	}
	repaired, err := storage.RepairHeapHeaders(rawA, []storage.RowVersion{b})
	if err != nil {
		t.Fatal(err)
	}
	actualB := repaired.Apply(b)
	c := storage.RowVersion{Row: 7, Version: 30, Base: b.Version, InsertFullXID: 40, PrivateHistory: storage.RetainHeapPredecessor(actualB)}
	rawC := storage.ReplaceHeapHeaders(repaired, []storage.RowVersion{c}, 40)
	if rawC.Header(b.Version).Successor != c.Version || rawC.Header(b.Version).Xmax != 40 || repaired.Header(b.Version).Successor != 0 || actualB.HeapXmax() != 0 {
		t.Fatal("repair self-link/captured header overwritten")
	}
	selectVersions := func(frontier ids.CommitSeq, committed, own []storage.RowVersion, headers *storage.HeapHeaderRoot) map[ids.VersionID]storage.RowVersion {
		t.Helper()
		source := &exec.Source{Committed: committed, Own: own, Headers: headers, Repaired: headers.Additional, NeedsBirthFilter: true}
		selected, err := exec.SelectQuerySource(context.Background(), source, frontier, nil, nil, visibility.HeapOperations{})
		if err != nil {
			t.Fatal(err)
		}
		result := make(map[ids.VersionID]storage.RowVersion, selected.Count())
		for i := range selected.Count() {
			row := selected.VersionAt(selected.RawIndex(i))
			if _, duplicate := result[row.Version]; duplicate {
				t.Fatal("duplicate physical version")
			}
			result[row.Version] = row
		}
		return result
	}
	own := selectVersions(1, []storage.RowVersion{a}, []storage.RowVersion{c}, rawC)
	if len(own) != 1 || own[c.Version].Version != c.Version {
		t.Fatal("private chain lost ordinary ancestor or repaired intermediate suppression", own)
	}
	headers := storage.CommitHeapEnds(rawC, []storage.RowVersion{a, actualB}, 40, 2)
	if headers.Header(a.Version).End != 2 || headers.Header(b.Version).End != 2 || rawC.Header(a.Version).End != 0 || rawC.Header(b.Version).End != 0 {
		t.Fatal("normal and repaired committed ends must share immutable publication")
	}
	c.Born, c.Base, c.PrivateHistory = 2, 0, nil
	latest := selectVersions(2, []storage.RowVersion{a, c}, nil, headers)
	if len(latest) != 1 || latest[c.Version].Version != c.Version {
		t.Fatal("committed ordinary ancestor remained visible", latest)
	}
	retained := selectVersions(1, []storage.RowVersion{a, c}, nil, headers)
	if len(retained) != 2 || retained[a.Version].Version != a.Version || retained[b.Version].HeapXmin() != 2 || retained[b.Version].HeapXmax() != 40 {
		t.Fatal("physical end retroactively changed older snapshot", retained)
	}
	if visibility.LiveVersion(headers.Apply(a), 2, nil, nil) || !visibility.LiveVersion(headers.Apply(a), 1, nil, nil) {
		t.Fatal("normal captured physical end ignored frontier")
	}
	// Even without repaired additions, generic selection must honor a captured
	// normal physical end instead of taking the old no-ends fast path.
	source := &exec.Source{Committed: []storage.RowVersion{a}, Headers: headers}
	selected, err := exec.SelectQuerySource(context.Background(), source, 2, nil, nil, visibility.HeapOperations{})
	if err != nil || selected.Count() != 0 {
		t.Fatal("normal-header fast path resurrected ended ancestor", selected, err)
	}
}

func TestPgsurgeryFreezeTupleAnchor(t *testing.T) {
	a := storage.RowVersion{Row: 7, Version: 10, Born: 1, InsertFullXID: 30}
	sibling := storage.RowVersion{Row: 7, Version: 11, Born: 2, InsertFullXID: 31}
	root, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{a})
	if err != nil {
		t.Fatal(err)
	}
	anchor := root.Header(a.Version)
	if anchor.TupleLockVersion != a.Version || anchor.TupleLockStamp != anchor.RepairStamp || anchor.TupleLockStamp == 0 {
		t.Fatal("repair did not assign exact anchor", anchor)
	}
	if got := root.Header(sibling.Version); got.TupleLockVersion != 0 || got.TupleLockStamp != 0 {
		t.Fatal("same-RowID sibling inherited unrelated repair", got)
	}
	b := storage.RowVersion{Row: a.Row, Version: 20, InsertFullXID: 40, PrivateHistory: storage.RetainHeapPredecessor(root.Apply(a))}
	linked := storage.ReplaceHeapHeaders(root, []storage.RowVersion{b}, 40)
	bHeader := linked.Header(b.Version)
	if bHeader.TupleLockVersion != anchor.TupleLockVersion || bHeader.TupleLockStamp != anchor.TupleLockStamp || bHeader.RepairStamp != 0 || bHeader.Repaired {
		t.Fatal("genuine successor lost anchor or became repaired", bHeader)
	}
	c := storage.RowVersion{Row: a.Row, Version: 30, InsertFullXID: 41, PrivateHistory: storage.RetainHeapPredecessor(linked.Apply(b))}
	chain := storage.ReplaceHeapHeaders(linked, []storage.RowVersion{c}, 41)
	if got := chain.Header(c.Version); got.TupleLockVersion != a.Version || got.TupleLockStamp != anchor.TupleLockStamp {
		t.Fatal("later genuine successor changed lineage", got)
	}
	again, err := storage.RepairHeapHeaders(chain, []storage.RowVersion{a})
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Header(a.Version); got.TupleLockVersion != a.Version || got.TupleLockStamp <= anchor.TupleLockStamp || got.Successor != 0 {
		t.Fatal("re-repair did not split only exact target", got)
	}
	if again.Header(b.Version) != chain.Header(b.Version) || again.Header(c.Version) != chain.Header(c.Version) || root.Header(a.Version) != anchor || root.Header(b.Version) != (storage.HeapHeader{}) {
		t.Fatal("re-repair changed successor or immutable capture")
	}
	stale := storage.RowVersion{Row: a.Row, Version: 40, PrivateHistory: storage.RetainHeapPredecessor(root.Apply(a))}
	if got := storage.ReplaceHeapHeaders(again, []storage.RowVersion{stale}, 42); got != again {
		t.Fatal("stale admission changed repaired anchor")
	}
	ended := storage.CommitHeapEnds(chain, []storage.RowVersion{chain.Apply(b)}, 41, 4)
	if got := ended.Header(b.Version); got.TupleLockVersion != a.Version || got.TupleLockStamp != anchor.TupleLockStamp || got.End != 4 {
		t.Fatal("real end lost tuple anchor", got)
	}
}
