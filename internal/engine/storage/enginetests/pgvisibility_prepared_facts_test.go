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

func TestPgvisibilityCheckPreparedFacts(t *testing.T) {
	low := visibilityFacts(t, 1, 1)[0]
	low.Version.Frozen = false
	low.Version.InsertFullXID = 10
	high := low
	high.Version.Version = 65536
	high.Version.Frozen = true
	headers := &storage.HeapHeaderRoot{}
	headers.Versions = headers.Versions.Set(65537, storage.HeapHeader{Slot: storage.HeapSlotDead})
	claims := storage.VisibilityMap{Runs: []storage.VisibilityRun{{End: 1, Bits: storage.VisibilityBits{AllVisible: true, AllFrozen: true}}}, Blocks: 1, MappedBlocks: 32672, AllVisible: 1, AllFrozen: 1}
	for _, input := range [][]storage.HeapTupleFact{{high, low, low}, {low, high, low}, {low, low, high}} {
		prepared := storage.PrepareVisibilityFacts(slices.Clone(input), headers)
		var versions []ids.VersionID
		for _, fact := range prepared {
			versions = append(versions, fact.Version.Version)
		}
		if !reflect.DeepEqual(versions, []ids.VersionID{1, 1, 65536, 65537}) || !prepared[3].LinePointerOnly || prepared[3].Version.Values != nil {
			t.Fatal("canonical physical identity/header-only facts", versions, prepared)
		}
		for _, canonical := range []bool{false, true} {
			var heap storage.VisibilityMap
			var err error
			if canonical {
				heap, err = prepared.Capture(t.Context(), storage.VisibilityHorizon{}, false)
			} else {
				arbitrary := slices.Clone([]storage.HeapTupleFact(prepared))
				slices.Reverse(arbitrary)
				heap, err = storage.CaptureVisibility(t.Context(), arbitrary, storage.VisibilityHorizon{}, false)
			}
			if err != nil || heap.Blocks != 1 || heap.MappedBlocks != 32672 || heap.AllVisible != 0 || heap.AllFrozen != 0 || heap.PageVisible(0) {
				t.Fatal("independent duplicate/dead-pointer READ layout", canonical, heap, err)
			}
			for _, frozen := range []bool{false, true} {
				want := []ids.VersionID{65537}
				if frozen {
					want = []ids.VersionID{1, 65537}
				}
				var got []ids.VersionID
				if canonical {
					got, err = prepared.Check(t.Context(), headers, claims, storage.VisibilityCheckHorizon{Oldest: 32}, frozen)
				} else {
					arbitrary := slices.Clone([]storage.HeapTupleFact(prepared))
					slices.Reverse(arbitrary)
					got, err = storage.CheckVisibility(t.Context(), arbitrary, headers, claims, storage.VisibilityCheckHorizon{Oldest: 32}, frozen)
				}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("independent canonical/standalone exact findings", canonical, frozen, got, err)
				}
			}
		}
		store := storage.Relation{Versions: []storage.RowVersion{low.Version, high.Version}, Headers: headers, Generation: 3}
		basis := prepared.Basis(&store)
		arbitrary := slices.Clone([]storage.HeapTupleFact(prepared))
		slices.Reverse(arbitrary)
		standalone := storage.CaptureVisibilityBasis(&store, arbitrary)
		if !basis.Same(&store, prepared) || !standalone.Same(&store, prepared) || len(basis.Facts) != 4 {
			t.Fatal("canonical standalone/basis ownership")
		}
		prepared[0].Version.Born++
		if basis.Same(&store, prepared) || standalone.Same(&store, prepared) {
			t.Fatal("basis borrowed mutable detached fact keys")
		}
	}
	// An existing real payload prevents a header-only duplicate from being added.
	covered := &storage.HeapHeaderRoot{}
	covered.Versions = covered.Versions.Set(1, storage.HeapHeader{Slot: storage.HeapSlotDead})
	payload := storage.PrepareVisibilityFacts([]storage.HeapTupleFact{low}, covered)
	if len(payload) != 1 || payload[0].LinePointerOnly || len(payload[0].Version.Values) != 1 {
		t.Fatal("real payload lost precedence")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := payload.Check(ctx, covered, claims, storage.VisibilityCheckHorizon{}, true); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("prepared cancellation published a prefix", got, err)
	}
	if got, err := payload.Capture(ctx, storage.VisibilityHorizon{}, false); !reflect.DeepEqual(got, storage.VisibilityMap{}) || !errors.Is(err, context.Canceled) {
		t.Fatal("prepared canceled READ prefix", got, err)
	}
}
