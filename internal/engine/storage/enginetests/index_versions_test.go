package enginetests

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func TestPgsurgeryFreezeIndexVersionsMetadata(t *testing.T) {
	entries := []storage.IndexEntry{{Row: 7, Version: 11, Position: 0}, {Row: 7, Version: 12, Position: 1}, {Row: 8, Version: 13, Position: 2}}
	root := &storage.IndexRoot{Entries: entries, Membership: make(map[ids.RowID]ids.VersionID), RowEntries: make(map[ids.RowID]int)}
	for at, entry := range entries {
		root.SetMember(entry.Row, entry.Version)
		root.SetRowEntry(at)
	}
	if root.MembershipLen() != 3 || root.RowEntryLen() != 3 || len(root.MembershipVersions) != 1 || len(root.RowEntryVersions) != 1 {
		t.Fatal("actual sibling metadata collapsed", root)
	}
	for at, entry := range entries {
		if !root.IsMember(entry.Row, entry.Version) {
			t.Fatal("exact member lost", entry)
		}
		if got, ok := root.RowEntry(entry.Row, entry.Version); !ok || got != at {
			t.Fatal("exact entry shadowed by sibling", entry, got, ok)
		}
	}
	if root.IsMember(7, 13) || root.IsMember(7, 99) || root.IsMember(0, 0) {
		t.Fatal("nonmember acquired authority")
	}
	if allocs := testing.AllocsPerRun(20, func() {
		if !root.IsMember(7, 11) || !root.IsMember(7, 12) {
			panic("missing exact membership")
		}
		if at, ok := root.RowEntry(7, 12); !ok || at != 1 {
			panic("missing exact ordinal")
		}
	}); allocs != 0 {
		t.Fatal("exact metadata lookup allocated", allocs)
	}
	candidate := storage.VacuumCandidate{Versions: []storage.RowVersion{{Row: 7, Version: 12}, {Row: 8, Version: 13}}, Positions: []int{-1, 0, 1}, Removed: 1}
	rows := []storage.RowVersion{{Row: 7, Version: 11}, {Row: 7, Version: 12}, {Row: 8, Version: 13}}
	next, err := storage.RemapVacuumIndex(context.Background(), root, rows, candidate, &storage.Relation{Versions: candidate.Versions}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if next.IsMember(7, 11) || !next.IsMember(7, 12) || next.MembershipLen() != 2 || next.RowEntryLen() != 2 {
		t.Fatal("compaction retired wrong sibling", next)
	}
	if at, ok := next.RowEntry(7, 12); !ok || at != 0 {
		t.Fatal("survivor ordinal was not remapped", at, ok)
	}
	if !root.IsMember(7, 11) || !root.IsMember(7, 12) || entries[1].Position != 1 {
		t.Fatal("immutable predecessor metadata mutated")
	}
	bucket := []storage.HashEntry{{Row: 7, Version: 11, Position: 0}, {Row: 7, Version: 12, Position: 1}, {Row: 8, Version: 13, Position: 2}}
	hash := &storage.HashRoot{Buckets: map[uint64][]storage.HashEntry{1: bucket}, RowEntries: make(map[ids.RowID]*storage.HashEntry), Prefix: 3}
	for at := range bucket {
		hash.SetRowEntry(&bucket[at])
	}
	if hash.RowEntry(7, 11) != &bucket[0] || hash.RowEntry(7, 12) != &bucket[1] || hash.RowEntry(7, 13) != nil {
		t.Fatal("hash exact entry lookup lost identity")
	}
	hashNext, err := storage.RemapVacuumIndex(context.Background(), &storage.IndexRoot{Hash: hash}, rows, candidate, &storage.Relation{Versions: candidate.Versions}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if hashNext.Hash.RowEntry(7, 11) != nil || hashNext.Hash.RowEntry(7, 12) != &hashNext.Hash.Buckets[1][0] || hash.RowEntry(7, 12) != &bucket[1] {
		t.Fatal("hash compaction lost exact pointer/backing ownership")
	}
}
