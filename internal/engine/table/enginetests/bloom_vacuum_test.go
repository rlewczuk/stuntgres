package enginetests_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
)

func TestBloomVacuumAuthority(t *testing.T) {
	for _, mode := range []string{"unchanged", "freeze", "retained"} {
		t.Run(mode, func(t *testing.T) {
			rows := []storage.RowVersion{{Row: 1, Version: 2, Born: 2, InsertFullXID: 4, Frozen: mode != "freeze"}}
			store := &storage.Relation{Versions: rows, Generation: 4}
			if mode == "retained" {
				store.Retained = &storage.HeapPredecessor{Version: storage.RowVersion{Row: 1, Version: 1, Born: 1}, End: 2}
			}
			prior := store.Retained
			before := slices.Clone(rows)
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}
			recipe := new(byte)
			root := &storage.IndexRoot{
				Construction: recipe,
				Entries:      []storage.IndexEntry{{Row: 1, Version: 2, Position: 0}},
				Bloom: &storage.BloomRoot{
					Storage: store, Generation: 4, Prefix: 1,
					Options: storage.BloomOptions{Words: 1},
					Entries: []storage.BloomEntry{{Signature: []uint16{7}}},
				},
			}
			capture := table.VacuumCapture{
				Rows: rows, Retained: prior, Generation: 4, Frontier: 2, Cutoff: 5, Retire: true,
				Relation: &catalog.Relation{}, Indexes: map[ids.ObjectAddress]*storage.IndexRoot{address: root},
			}
			var phases []table.VacuumProgress
			pass, err := table.PrepareVacuumPass(context.Background(), capture, store, func(p table.VacuumProgress) { phases = append(phases, p) })
			if err != nil || pass.Candidate.Positions != nil {
				t.Fatal("noncompacting pass", pass, err)
			}
			if mode == "unchanged" {
				if pass.Generation != 4 || len(pass.Roots) != 0 {
					t.Fatal("unchanged heap allocated index replacements", pass)
				}
			} else {
				next := pass.Roots[address]
				if pass.Generation != 5 || len(pass.Roots) != 1 || next == nil || next == root || next.Bloom == root.Bloom || next.Construction != recipe {
					t.Fatal("changed heap omitted detached bloom rebind", pass)
				}
				if next.Bloom.Storage != store || next.Bloom.Generation != 5 || next.Bloom.Prefix != 1 || next.Bloom.Ends != pass.Candidate.Ends || &next.Entries[0] != &root.Entries[0] || &next.Bloom.Entries[0].Signature[0] != &root.Bloom.Entries[0].Signature[0] {
					t.Fatal("rebind changed immutable membership/signatures or lost coverage", next)
				}
				completed := false
				for _, phase := range phases {
					if phase.Phase == "vacuuming indexes" && phase.IndexesTotal == 1 && phase.IndexesProcessed == 1 {
						completed = true
					}
				}
				if !completed || mode == "freeze" && pass.Candidate.Frozen != 1 || mode == "retained" && (pass.Candidate.Removed != 1 || pass.Candidate.Retained != nil) {
					t.Fatal("pass did not complete actual maintenance/index work", pass, phases)
				}
			}
			if store.Generation != 4 || store.Retained != prior || !reflect.DeepEqual(rows, before) || root.Bloom.Generation != 4 || root.Bloom.Prefix != 1 || root.Bloom.Entries[0].Signature[0] != 7 {
				t.Fatal("detached maintenance mutated predecessor authority")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if failed, err := table.PrepareVacuumPass(ctx, capture, store, func(table.VacuumProgress) {}); err != context.Canceled || failed.Roots != nil {
				t.Fatal("canceled pass returned publishable roots", failed, err)
			}
		})
	}
}

func TestBloomVacuumPrefix(t *testing.T) {
	rows := []storage.RowVersion{{Row: 1, Version: 1}, {Row: 2, Version: 2}, {Row: 3, Version: 3}}
	store := &storage.Relation{Versions: rows, Generation: 4}
	root := &storage.IndexRoot{
		Entries: []storage.IndexEntry{{Row: 2, Version: 2, Position: 1}},
		Bloom: &storage.BloomRoot{
			Storage: store, Generation: 4, Prefix: 2,
			Entries: []storage.BloomEntry{{Signature: []uint16{7}}},
		},
	}
	candidate := storage.VacuumCandidate{Versions: rows[1:], Positions: []int{-1, 0, 1}, Ends: &storage.DeletionRoot{}}
	next, err := storage.RemapVacuumIndex(context.Background(), root, rows, candidate, store, 5)
	if err != nil || next.Bloom.Prefix != 1 || next.Bloom.Ends != candidate.Ends || next.Entries[0].Position != 0 || &next.Bloom.Entries[0].Signature[0] != &root.Bloom.Entries[0].Signature[0] {
		t.Fatal("compaction promoted incomplete coverage or rehashed survivor", next, err)
	}
	candidate = storage.VacuumCandidate{Versions: slices.Clone(rows)}
	next, err = storage.RemapVacuumIndex(context.Background(), root, rows, candidate, store, 5)
	if err != nil || next.Bloom.Prefix != 2 || next.Bloom.Generation != 5 || &next.Entries[0] != &root.Entries[0] || &next.Bloom.Entries[0] != &root.Bloom.Entries[0] {
		t.Fatal("noncompacting rebind changed coverage/backing", next, err)
	}
	if root.Bloom.Generation != 4 || root.Bloom.Prefix != 2 || root.Entries[0].Position != 1 {
		t.Fatal("remap mutated retained source")
	}
}
