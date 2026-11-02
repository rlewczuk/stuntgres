package enginetests

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func visibilityFacts(t *testing.T, count, width int) []storage.HeapTupleFact {
	t.Helper()
	values := make([]sqltype.Value, width)
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	value, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := range values {
		values[i] = value
	}
	facts := make([]storage.HeapTupleFact, count)
	for i := range facts {
		facts[i].Version = storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Born: 11, Frozen: true, Values: values}
	}
	return facts
}
func TestPgvisibilityLayout(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		count, width int
		blocks       int64
	}{{0, 0, 0}, {291, 0, 1}, {292, 0, 2}, {185, 1, 1}, {186, 1, 2}, {1, 1200, 2}} {
		facts := visibilityFacts(t, tc.count, tc.width)
		result, err := storage.CaptureVisibility(ctx, facts, storage.VisibilityHorizon{}, false)
		if err != nil || result.Blocks != tc.blocks || result.AllVisible != tc.blocks || result.AllFrozen != tc.blocks {
			t.Fatal(tc, result, err)
		}
		if tc.blocks != 0 && result.MappedBlocks != 32672 {
			t.Fatal("genuine frozen coverage", result)
		}
		if result.Probe(tc.blocks) != (storage.VisibilityBits{}) || result.Probe(-1) != (storage.VisibilityBits{}) {
			t.Fatal("beyond EOF grew the heap", result)
		}
		for block := int64(0); block < tc.blocks; block++ {
			if result.Probe(block) != (storage.VisibilityBits{AllVisible: true, AllFrozen: true}) {
				t.Fatal("continuation lost flags", block, result)
			}
		}
		reversed := slices.Clone(facts)
		slices.Reverse(reversed)
		permuted, err := storage.CaptureVisibility(ctx, reversed, storage.VisibilityHorizon{}, false)
		if err != nil || !reflect.DeepEqual(result, permuted) {
			t.Fatal("VersionID layout depends on input order", result, permuted, err)
		}
	}
	facts := visibilityFacts(t, 1, 1200)
	duplicate := append(slices.Clone(facts), facts[0])
	one, err := storage.CaptureVisibility(ctx, duplicate, storage.VisibilityHorizon{}, false)
	if err != nil || one.Blocks != 2 {
		t.Fatal("same identity consumed two allocations", one, err)
	}
	duplicate[1].Version.Version = 2
	two, err := storage.CaptureVisibility(ctx, duplicate, storage.VisibilityHorizon{}, false)
	if err != nil || two.Blocks != 3 {
		t.Fatal("distinct physical predecessors were collapsed", two, err)
	}
	for _, tc := range []struct{ blocks, want int64 }{{0, 0}, {1, 32672}, {32672, 32672}, {32673, 65344}} {
		got, err := storage.VisibilityVMExtent(tc.blocks)
		if err != nil || got != tc.want {
			t.Fatal("VM page boundary", tc, got, err)
		}
	}
	for _, bad := range []int64{-1, math.MaxInt64} {
		_, err := storage.VisibilityVMExtent(bad)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" {
			t.Fatal("unchecked VM extent", bad, err)
		}
	}
	if _, err := storage.CatalogVisibility(-1); err == nil {
		t.Fatal("negative represented bytes accepted")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	got, err := storage.CaptureVisibility(ctx, visibilityFacts(t, 2, 1), storage.VisibilityHorizon{}, false)
	if !errors.Is(err, context.Canceled) || got.Blocks != 0 || got.Runs != nil {
		t.Fatal("canceled classification published prefix", got, err)
	}
}

func TestPgvisibilityClassification(t *testing.T) {
	domain := new(ids.Domain)
	owner := domain.Tx(domain.Session(1), 1, 1, nil, 0)
	base := visibilityFacts(t, 1, 1)[0]
	for _, tc := range []struct {
		name         string
		mutate       func(*storage.HeapTupleFact)
		horizon      storage.VisibilityHorizon
		unknownLocks bool
		av, af       bool
	}{
		{"frozen", func(*storage.HeapTupleFact) {}, storage.VisibilityHorizon{}, false, true, true},
		{"ordinary_frozen_raw_xmin", func(f *storage.HeapTupleFact) { f.Version.InsertFullXID = uint64(1)<<32 | 101 }, storage.VisibilityHorizon{}, false, true, true},
		{"raw_frozen_id_without_flag", func(f *storage.HeapTupleFact) { f.Version.Frozen = false; f.Version.InsertFullXID = 2 }, storage.VisibilityHorizon{}, false, false, false},
		{"uncommitted_even_frozen", func(f *storage.HeapTupleFact) { f.Version.Born = 0 }, storage.VisibilityHorizon{}, false, false, false},
		{"inserter", func(f *storage.HeapTupleFact) { f.Inserter = owner }, storage.VisibilityHorizon{}, false, false, false},
		{"ender", func(f *storage.HeapTupleFact) { f.Ender = owner }, storage.VisibilityHorizon{}, false, false, false},
		{"committed_end", func(f *storage.HeapTupleFact) { f.End = 12 }, storage.VisibilityHorizon{}, false, false, false},
		{"unfrozen", func(f *storage.HeapTupleFact) { f.Version.Frozen = false }, storage.VisibilityHorizon{}, false, false, false},
		{"old_commit_frontier", func(*storage.HeapTupleFact) {}, storage.VisibilityHorizon{Protected: true, Frontier: 10}, false, false, false},
		{"inclusive_frontier", func(*storage.HeapTupleFact) {}, storage.VisibilityHorizon{Protected: true, Frontier: 11}, false, true, true},
		{"zero_frontier", func(*storage.HeapTupleFact) {}, storage.VisibilityHorizon{Protected: true, Frontier: 0}, false, false, false},
		{"unknown_peer", func(*storage.HeapTupleFact) {}, storage.VisibilityHorizon{Unknown: true}, false, false, false},
		{"import_frozen", func(f *storage.HeapTupleFact) { f.Version.Frozen = false; f.Version.ImportFrozen = true }, storage.VisibilityHorizon{}, false, true, true},
		{"import_protected", func(f *storage.HeapTupleFact) { f.Version.Frozen = false; f.Version.ImportFrozen = true }, storage.VisibilityHorizon{Protected: true, Frontier: 10}, false, false, false},
		{"real_lock", func(f *storage.HeapTupleFact) { f.LockMaintenance = true }, storage.VisibilityHorizon{}, false, true, false},
		{"unknown_lock_proof", func(*storage.HeapTupleFact) {}, storage.VisibilityHorizon{}, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fact := base
			tc.mutate(&fact)
			got, err := storage.CaptureVisibility(context.Background(), []storage.HeapTupleFact{fact}, tc.horizon, tc.unknownLocks)
			if err != nil || got.Probe(0) != (storage.VisibilityBits{AllVisible: tc.av, AllFrozen: tc.af}) || got.AllVisible != boolInt(tc.av) || got.AllFrozen != boolInt(tc.af) {
				t.Fatal(got, err)
			}
			// VM layout follows genuine frozen provenance even if snapshot/lock
			// protection clears positive bits; no result-driven fork toggle.
			if (fact.Version.Frozen || fact.Version.ImportFrozen) && got.MappedBlocks != 32672 {
				t.Fatal("false flags discarded genuine VM extent", got)
			}
		})
	}
	facts := visibilityFacts(t, 3, 1200)
	facts[1].Ender = owner
	got, err := storage.CaptureVisibility(context.Background(), facts, storage.VisibilityHorizon{}, false)
	if err != nil || got.Blocks != 4 || got.AllVisible != 2 || got.AllFrozen != 2 {
		t.Fatal("continuation aggregate", got, err)
	}
	for block, want := range []bool{true, false, false, true} {
		if got.Probe(int64(block)).AllVisible != want {
			t.Fatal("one unresolved fact failed to contaminate every intersected block", block, got)
		}
	}
	catalog, err := storage.CatalogVisibility(9780)
	if err != nil || catalog.Blocks != 2 || catalog.AllVisible != 0 || catalog.AllFrozen != 0 || catalog.MappedBlocks != 0 {
		t.Fatal("catalog provenance fabricated", catalog, err)
	}
}
func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
