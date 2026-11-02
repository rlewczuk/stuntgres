package enginetests

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/util"
)

// Every accepted row may expose an immutable intermediate root. Publication must
// scale with changed trie paths, not copy an ever-growing complete header map.
func TestPgsurgeryFreezeHeaderPublicationAllocation(t *testing.T) {
	for _, operation := range []string{"replace", "end", "commit"} {
		t.Run(operation, func(t *testing.T) {
			var previous int64
			for _, n := range []int{128, 512, 2048} {
				rows := make([]storage.RowVersion, n)
				successors := make([]storage.RowVersion, n)
				captures := make([]*storage.HeapHeaderRoot, n)
				for i := range rows {
					rows[i] = storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(2*i + 1), Born: 1, InsertFullXID: 30}
					successors[i] = storage.RowVersion{Row: rows[i].Row, Version: rows[i].Version + 1, InsertFullXID: 40, PrivateHistory: storage.RetainHeapPredecessor(rows[i])}
				}
				_, bytes := util.AllocationsPerCall(3, func() {
					var root *storage.HeapHeaderRoot
					for i := range rows {
						switch operation {
						case "replace":
							root = storage.ReplaceHeapHeaders(root, successors[i:i+1], 40)
						case "end":
							root = storage.EndHeapHeaders(root, rows[i:i+1], 40)
						case "commit":
							root = storage.CommitHeapEnds(root, rows[i:i+1], 40, 2)
						}
						captures[i] = root
					}
				})
				// Keep the same growth envelope as the composed multirow UPDATE
				// witness: four times the rows allows six times the bytes plus
				// fixed allocator noise, not quadratic whole-map copies.
				if previous != 0 && bytes > previous*6+(1<<20) {
					t.Fatal("header publication exceeds linear growth envelope", n, previous, bytes)
				}
				previous = bytes
				for i, captured := range captures {
					want := storage.HeapHeader{Xmax: 40}
					if operation == "replace" {
						want.Successor = successors[i].Version
					} else if operation == "commit" {
						want.End = 2
					}
					if captured == nil || captured.Versions.Len() != i+1 || captured.Header(rows[i].Version) != want || captured.Stamp != 0 || len(captured.Additional) != 0 {
						t.Fatal("intermediate publication lost exact ordinary header", operation, n, i, captured)
					}
					if i+1 < n && captured.Header(rows[i+1].Version) != (storage.HeapHeader{}) {
						t.Fatal("later publication mutated captured header root", operation, n, i)
					}
					first := captured.Header(rows[0].Version)
					if first != captures[0].Header(rows[0].Version) {
						t.Fatal("unmodified earlier header changed", operation, n, i, first)
					}
					if operation != "replace" && captured.Apply(rows[i]).ForcedEnd != want.End {
						t.Fatal("end publication changed captured visibility end", operation, n, i)
					}
				}
			}
		})
	}
}

func TestPgsurgeryFreezeHeaderNoopAllocation(t *testing.T) {
	row := storage.RowVersion{Row: 1, Version: 1, Born: 1, InsertFullXID: 30}
	root, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{row})
	if err != nil {
		t.Fatal(err)
	}
	stale := []storage.RowVersion{row}
	staleSuccessor := []storage.RowVersion{{Row: 1, Version: 2, PrivateHistory: storage.RetainHeapPredecessor(row)}}
	plain := []storage.RowVersion{{Row: 2, Version: 3}}
	var absent *storage.HeapHeaderRoot
	allocations, bytes := util.AllocationsPerCall(100, func() {
		if absent.Header(row.Version) != (storage.HeapHeader{}) || absent.Apply(row).Version != row.Version ||
			storage.EndHeapHeaders(absent, nil, 40) != nil || storage.CommitHeapEnds(absent, nil, 40, 2) != nil ||
			storage.ReplaceHeapHeaders(absent, plain, 40) != nil {
			t.Fatal("ordinary empty header path changed nil-root semantics")
		}
		if next, err := storage.RepairHeapHeaders(root, nil); err != nil || next != root {
			t.Fatal("empty repair changed captured root", err)
		}
		if storage.EndHeapHeaders(root, stale, 40) != root || storage.CommitHeapEnds(root, stale, 40, 2) != root ||
			storage.ReplaceHeapHeaders(root, staleSuccessor, 40) != root || storage.ReplaceHeapHeaders(root, plain, 40) != root {
			t.Fatal("unadmitted writer replaced current repaired root")
		}
	})
	if allocations != 0 || bytes != 0 {
		t.Fatal("unchanged header path allocated", allocations, bytes)
	}
}
