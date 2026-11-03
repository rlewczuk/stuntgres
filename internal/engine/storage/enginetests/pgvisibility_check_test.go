package enginetests

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func TestPgvisibilityCheckTruth(t *testing.T) {
	domain := new(ids.Domain)
	owner := domain.Tx(domain.Session(1), 1, 1, nil, 0)
	base := visibilityFacts(t, 1, 1)[0]
	base.Version.InsertFullXID = 31
	for _, tc := range []struct {
		name            string
		change          func(*storage.HeapTupleFact)
		horizon         storage.VisibilityCheckHorizon
		visible, frozen bool
	}{
		{"healthy_frozen", func(*storage.HeapTupleFact) {}, storage.VisibilityCheckHorizon{Oldest: 32}, false, false},
		{"old_committed_unfrozen", func(f *storage.HeapTupleFact) { f.Version.Frozen = false; f.Version.Born = 9000 }, storage.VisibilityCheckHorizon{Oldest: 32}, false, true},
		{"horizon_equality", func(f *storage.HeapTupleFact) { f.Version.Frozen = false }, storage.VisibilityCheckHorizon{Oldest: 31}, true, true},
		{"newer_insertion", func(f *storage.HeapTupleFact) { f.Version.Frozen = false }, storage.VisibilityCheckHorizon{Oldest: 30}, true, true},
		{"unknown_horizon", func(f *storage.HeapTupleFact) { f.Version.Frozen = false }, storage.VisibilityCheckHorizon{Unknown: true}, false, true},
		{"private_insertion", func(f *storage.HeapTupleFact) { f.Version.Born = 0; f.Version.Frozen = false; f.Inserter = owner }, storage.VisibilityCheckHorizon{Oldest: 32}, true, true},
		{"pending_delete", func(f *storage.HeapTupleFact) { f.Ender = owner }, storage.VisibilityCheckHorizon{Oldest: 32}, true, true},
		{"committed_delete", func(f *storage.HeapTupleFact) { f.End = 12; f.Version.DeleteFullXID = 32 }, storage.VisibilityCheckHorizon{Oldest: 33}, true, true},
		{"retained_raw_xmax", func(f *storage.HeapTupleFact) { f.Version.DeleteFullXID = 32 }, storage.VisibilityCheckHorizon{Oldest: 33}, false, true},
		{"genuine_lock_maintenance", func(f *storage.HeapTupleFact) { f.LockMaintenance = true }, storage.VisibilityCheckHorizon{Oldest: 33}, false, true},
		{"unknown_tuple_provenance", func(f *storage.HeapTupleFact) {
			f.Version.Born = 0
			f.Version.Frozen = false
			f.Version.InsertFullXID = 0
		}, storage.VisibilityCheckHorizon{Oldest: 32}, false, false},
		{"discarded", func(f *storage.HeapTupleFact) { f.Aborted = true; f.Version.Frozen = false }, storage.VisibilityCheckHorizon{Oldest: 32}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fact := base
			tc.change(&fact)
			if got := storage.VisibilityContradiction(fact, tc.horizon, false, storage.HeapSlotNormal); got != tc.visible {
				t.Fatal("visible truth", got, tc.visible)
			}
			if got := storage.VisibilityContradiction(fact, tc.horizon, true, storage.HeapSlotNormal); got != tc.frozen {
				t.Fatal("frozen truth", got, tc.frozen)
			}
			for _, slot := range []storage.HeapSlotState{storage.HeapSlotUnused, storage.HeapSlotRedirect, storage.HeapSlotDead} {
				for _, frozen := range []bool{false, true} {
					if got := storage.VisibilityContradiction(fact, tc.horizon, frozen, slot); got != (slot == storage.HeapSlotDead) {
						t.Fatal("slot state", slot, frozen, got)
					}
				}
			}
		})
	}
	// The actual shared layout puts this one physical allocation across two
	// modeled pages. Duplicate observations and same-RowID predecessors are
	// distinguished by VersionID, never by byte-page coordinates or DISTINCT.
	facts := visibilityFacts(t, 1, 1200)
	facts[0].Version.Frozen = false
	facts[0].Version.InsertFullXID = 31
	facts[0].Version.Version = 65536
	facts = append(facts, facts[0])
	claims := storage.VisibilityMap{Runs: []storage.VisibilityRun{{End: 2, Bits: storage.VisibilityBits{AllVisible: true, AllFrozen: true}}}, Blocks: 2, MappedBlocks: 32672, AllVisible: 2, AllFrozen: 2}
	found, err := storage.CheckVisibility(t.Context(), facts, nil, claims, storage.VisibilityCheckHorizon{Oldest: 32}, true)
	if err != nil || !reflect.DeepEqual(found, []ids.VersionID{65536}) {
		t.Fatal("continuation/version dedup", found, err)
	}
	block, offset, err := storage.LogicalTID(found[0])
	if err != nil || block != 1 || offset != 1 {
		t.Fatal("exact independent TID", block, offset, err)
	}
	block, offset, err = storage.LogicalTID(1)
	if err != nil || block != 0 || offset != 1 {
		t.Fatal("first bijection", block, offset, err)
	}
	facts = facts[:1]
	second := facts[0]
	second.Version.Version = 65537
	facts = append(facts, second)
	claims.Runs[0].End = 3
	claims.Blocks = 3
	found, err = storage.CheckVisibility(t.Context(), facts, nil, claims, storage.VisibilityCheckHorizon{Oldest: 32}, true)
	if err != nil || !reflect.DeepEqual(found, []ids.VersionID{65536, 65537}) {
		t.Fatal("same logical row distinct versions", found, err)
	}
	claims.Runs[0].Bits = storage.VisibilityBits{}
	found, err = storage.CheckVisibility(t.Context(), facts, nil, claims, storage.VisibilityCheckHorizon{Oldest: 1}, true)
	if err != nil || len(found) != 0 {
		t.Fatal("unmarked bad tuples", found, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	found, err = storage.CheckVisibility(ctx, facts, nil, claims, storage.VisibilityCheckHorizon{}, true)
	if !errors.Is(err, context.Canceled) || found != nil {
		t.Fatal("canceled prefix", found, err)
	}
	t.Run("payload_free_dead_line_pointer", func(t *testing.T) {
		headers := &storage.HeapHeaderRoot{}
		headers.Versions = headers.Versions.Set(65536, storage.HeapHeader{Slot: storage.HeapSlotDead})
		headers.Versions = headers.Versions.Set(65537, storage.HeapHeader{Slot: storage.HeapSlotUnused})
		headers.Versions = headers.Versions.Set(65538, storage.HeapHeader{Slot: storage.HeapSlotRedirect})
		prepared := storage.PrepareVisibilityFacts(nil, headers)
		if len(prepared) != 1 || !prepared[0].LinePointerOnly || prepared[0].Version.Version != 65536 || prepared[0].Version.Row != 0 || prepared[0].Version.Born != 0 || prepared[0].Version.Values != nil {
			t.Fatal("invented tuple payload/provenance", prepared)
		}
		heap, err := storage.CaptureVisibility(t.Context(), prepared, storage.VisibilityHorizon{}, false)
		if err != nil || heap.Blocks != 1 || heap.AllVisible != 0 || heap.AllFrozen != 0 || heap.MappedBlocks != 0 || heap.PageVisible(0) {
			t.Fatal("dead pointer established tuple/PD/frozen proof", heap, err)
		}
		claims := storage.VisibilityMap{Runs: []storage.VisibilityRun{{End: 1, Bits: storage.VisibilityBits{AllVisible: true, AllFrozen: true}}}, Blocks: 1, MappedBlocks: 32672, AllVisible: 1, AllFrozen: 1}
		for _, frozen := range []bool{false, true} {
			found, err := storage.CheckVisibility(t.Context(), prepared, headers, claims, storage.VisibilityCheckHorizon{}, frozen)
			if err != nil || !reflect.DeepEqual(found, []ids.VersionID{65536}) {
				t.Fatal("claimed genuine dead slot", found, err)
			}
		}
		payload := visibilityFacts(t, 1, 1)
		payload[0].Version.Version = 65536
		dedup := storage.PrepareVisibilityFacts(payload, headers)
		if len(dedup) != 1 || dedup[0].LinePointerOnly {
			t.Fatal("real physical payload double counted", dedup)
		}
		for _, count := range []int{2042, 2043} {
			headers := &storage.HeapHeaderRoot{}
			for i := range count {
				headers.Versions = headers.Versions.Set(ids.VersionID(i+1), storage.HeapHeader{Slot: storage.HeapSlotDead})
			}
			facts := storage.PrepareVisibilityFacts(nil, headers)
			heap, err := storage.CaptureVisibility(t.Context(), facts, storage.VisibilityHorizon{}, false)
			want := int64(1)
			if count == 2043 {
				want = 2
			}
			if err != nil || heap.Blocks != want {
				t.Fatal("line pointer is not exactly four bytes", count, heap, err)
			}
		}
	})
}

func TestPgvisibilityCheckMapState(t *testing.T) {
	facts := visibilityFacts(t, 1, 1200)
	rows := []storage.RowVersion{facts[0].Version}
	store := storage.Relation{Versions: rows, Generation: 7}
	heap, err := storage.CaptureVisibility(t.Context(), facts, storage.VisibilityHorizon{}, false)
	if err != nil {
		t.Fatal(err)
	}
	root := &storage.VisibilityRoot{Map: heap, Basis: storage.CaptureVisibilityBasis(&store, facts)}
	store.Visibility = root
	if !root.Basis.Same(&store, facts) || root.Map.Blocks != 2 || root.Map.AllVisible != 2 || root.Map.AllFrozen != 2 || root.Map.MappedBlocks != 32672 {
		t.Fatal("actual materialized roots", root)
	}
	oldRuns := slices.Clone(root.Map.Runs)
	store.Visibility = storage.EmptyVisibilityRoot()
	store.Generation++
	store.Headers = &storage.HeapHeaderRoot{}
	copy := store
	for _, current := range []*storage.Relation{&store, &copy} {
		if !current.Visibility.Invalidated || root.Basis.Same(current, facts) {
			t.Fatal("invalidation/basis lost")
		}
		result := storage.SuppressedVisibility(heap)
		if result.AllVisible != 0 || result.AllFrozen != 0 || result.MappedBlocks != 0 || result.Blocks != 2 {
			t.Fatal("empty compressed VM", result)
		}
		for block := int64(0); block < 2; block++ {
			if result.Probe(block) != (storage.VisibilityBits{}) || !result.PageVisible(block) {
				t.Fatal("independent PD", block, result)
			}
		}
		if result.PageVisible(2) {
			t.Fatal("PD beyond physical domain")
		}
		if !reflect.DeepEqual(root.Map.Runs, oldRuns) || root.Invalidated {
			t.Fatal("old root mutated")
		}
	}
	// A same-layout private-version change cannot borrow an old marked basis.
	store.Generation = 7
	store.Headers = nil
	changed := slices.Clone(facts)
	changed[0].End = 12
	if root.Basis.Same(&store, changed) {
		t.Fatal("private end reused claims")
	}
	store.Visibility = storage.EmptyVisibilityRoot()
	if store.Visibility != storage.EmptyVisibilityRoot() {
		t.Fatal("idempotent empty root")
	}
	t.Run("nullable_preserving_publication_observation", func(t *testing.T) {
		// A nil VM observation is real: an actual preserving copy of it must
		// not erase later physical invalidation even at an unchanged generation.
		source := storage.Relation{Generation: ^uint64(0)}
		transport := source
		shape := storage.RowShape{Original: &source, Replacement: &transport, Generation: source.Generation, PreservesVisibility: true}
		if !shape.MatchesWithEnds(&source) {
			t.Fatal("unchanged nil observation rejected")
		}
		source.Visibility = storage.EmptyVisibilityRoot()
		if shape.MatchesWithEnds(&source) || transport.Visibility != nil {
			t.Fatal("nil predecessor VM was not fenced immutably")
		}
		metadata := shape
		metadata.Replacement = &source
		metadata.PreservesVisibility = false
		if !metadata.MatchesWithEnds(&source) {
			t.Fatal("same-store metadata spuriously fenced VM")
		}
		// Terminal cleanup may create a destructive fresh heap at exhausted
		// generation without incrementing it; equality alone is not provenance.
		fresh := storage.Relation{Generation: source.Generation}
		destructive := shape
		destructive.Replacement = &fresh
		destructive.PreservesVisibility = false
		if !destructive.MatchesWithEnds(&source) {
			t.Fatal("fresh same-generation heap was mistaken for a codec copy")
		}
	})
}
