package enginetests

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func deletionSiblingHeap(t *testing.T) (*storage.Relation, storage.RowVersion, storage.RowVersion) {
	t.Helper()
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			break
		}
	}
	a, err := sqltype.Integer(integer, 11)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sqltype.Integer(integer, 22)
	if err != nil {
		t.Fatal(err)
	}
	var allocator storage.Store
	rows, err := allocator.AllocateRowVersionsLocked([][]sqltype.Value{{a}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := rows[0]
	first.Born = 1
	second := storage.RowVersion{Row: first.Row, Version: allocator.AllocateVersionLocked(), Born: 2, Values: []sqltype.Value{b}}
	return &storage.Relation{Versions: []storage.RowVersion{first, second}}, first, second
}

func TestDeletionRootVersionCollisions(t *testing.T) {
	store, first, second := deletionSiblingHeap(t)
	var absent *storage.DeletionRoot
	if absent.Len() != 0 || absent.Clone() != nil {
		t.Fatal("nil end root acquired ownership")
	}
	for range absent.All() {
		t.Fatal("nil end root enumerated a version")
	}
	root := &storage.DeletionRoot{}
	root.Set(first.Row, storage.RowEnd{Version: first.Version, End: 3, EndFullXID: 101})
	root.Set(second.Row, storage.RowEnd{Version: second.Version, End: 6, EndFullXID: 102, Moved: true})
	store.Ends = root
	if root.Len() != 2 || len(root.Rows) != 1 || len(root.Versions) != 1 {
		t.Fatal("same-row ends do not have disjoint single authorities")
	}
	if _, ok := root.Lookup(first.Row, second.Version+1); ok {
		t.Fatal("unknown physical version fell back to a row's end")
	}
	if _, ok := root.Lookup(first.Row+1, second.Version); ok {
		t.Fatal("physical end rebound to a different row")
	}
	seen := make(map[ids.VersionID]bool)
	for row, end := range root.All() {
		if row != first.Row || end.Row != row || seen[end.Version] {
			t.Fatal("ambiguous/duplicate exact end enumeration")
		}
		seen[end.Version] = true
	}
	if !seen[first.Version] || !seen[second.Version] {
		t.Fatal("supplementary sibling end disappeared")
	}
	stopped := 0
	for range root.All() {
		stopped++
		break
	}
	if stopped != 1 {
		t.Fatal("end iteration ignored early stop")
	}
	clone := root.Clone()
	clone.Set(first.Row, storage.RowEnd{Version: first.Version, End: 9})
	clone.Set(second.Row, storage.RowEnd{Version: second.Version, End: 10})
	oldFirst, _ := root.Lookup(first.Row, first.Version)
	oldSecond, _ := root.Lookup(second.Row, second.Version)
	if oldFirst.End != 3 || oldFirst.EndFullXID != 101 || oldSecond.End != 6 || !oldSecond.Moved || clone.Len() != 2 {
		t.Fatal("cloned end updates mutated a retained root or duplicated a version")
	}
	clone.Delete(first.Row, first.Version)
	if _, ok := clone.Lookup(first.Row, first.Version); ok {
		t.Fatal("exact end removal kept its selected version")
	}
	if sibling, ok := clone.Lookup(second.Row, second.Version); !ok || sibling.End != 10 || clone.Len() != 1 {
		t.Fatal("removing primary end erased its colliding sibling")
	}
	clone.Delete(second.Row+1, second.Version)
	if clone.Len() != 1 || root.Len() != 2 {
		t.Fatal("wrong-row removal or clone altered physical ownership")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if _, ok := root.Lookup(first.Row, first.Version); !ok {
			panic("missing primary end")
		}
	}); allocations != 0 {
		t.Fatal("ordinary exact end lookup allocated", allocations)
	}
}

func TestDeletionRootVacuumSiblingTransport(t *testing.T) {
	store, first, second := deletionSiblingHeap(t)
	root := &storage.DeletionRoot{}
	root.Set(first.Row, storage.RowEnd{Version: first.Version, End: 3})
	root.Set(second.Row, storage.RowEnd{Version: second.Version, End: 6, Moved: true})
	store.Ends = root
	captured := *store
	inspected, err := storage.InspectVacuum(context.Background(), store.Versions, root, 4, 0, true, nil)
	if err != nil || inspected.Removed != 1 {
		t.Fatal("vacuum did not discriminate sibling end frontiers", inspected, err)
	}
	compacted, err := storage.MaterializeVacuum(context.Background(), store.Versions, root, 4, 0, inspected, nil)
	if err != nil || len(compacted.Versions) != 1 || compacted.Versions[0].Version != second.Version || compacted.Ends.Len() != 1 {
		t.Fatal("compaction lost the live sibling or retained wrong end", compacted, err)
	}
	if end, ok := compacted.Ends.Lookup(second.Row, second.Version); !ok || end.End != 6 || !end.Moved {
		t.Fatal("compaction dropped supplementary same-row end")
	}
	for _, retire := range []bool{false, true} {
		rewritten, err := storage.RewriteVacuum(context.Background(), store.Versions, root, nil, 4, 0, retire, nil)
		want := 2
		if retire {
			want = 1
		}
		if err != nil || len(rewritten.Versions) != want || rewritten.Ends.Len() != want {
			t.Fatal("rewrite failed exact sibling end transport", rewritten, err)
		}
		if end, ok := rewritten.Ends.Lookup(second.Row, second.Version); !ok || end.End != 6 || !end.Moved {
			t.Fatal("rewrite rebound/dropped sibling end")
		}
	}
	if root.Len() != 2 || captured.Ends != root || !storage.SameVersionRoot(captured.Versions, store.Versions) || &compacted.Versions[0].Values[0] != &second.Values[0] || &captured.Versions[0].Values[0] != &first.Values[0] {
		t.Fatal("maintenance mutated old roots or copied immutable payload backing")
	}
}
