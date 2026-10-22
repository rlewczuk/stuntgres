package visibility

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func rowVersions(pairs ...int) []storage.RowVersion {
	rows := make([]storage.RowVersion, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		rows = append(rows, storage.RowVersion{Row: ids.RowID(pairs[i]), Version: ids.VersionID(pairs[i+1])})
	}
	return rows
}

func sameRows(t *testing.T, label string, got []storage.RowVersion, want ...int) {
	t.Helper()
	expected := rowVersions(want...)
	if len(got) != len(expected) {
		t.Fatalf("%s: %d rows, want %d", label, len(got), len(expected))
	}
	for i := range got {
		if got[i].Row != expected[i].Row || got[i].Version != expected[i].Version {
			t.Fatalf("%s: row %d is %d/%d, want %d/%d", label, i, got[i].Row, got[i].Version, expected[i].Row, expected[i].Version)
		}
	}
}

// TestWriteSetMergesKeepRetainedPrefixes pins the copy-on-write contract of
// the private write-set merges: a replacement never writes into a slice an
// earlier view retains, and an insert-only merge appends.
func TestWriteSetMergesKeepRetainedPrefixes(t *testing.T) {
	old := rowVersions(1, 10, 2, 20, 3, 30)
	sameRows(t, "empty old", MergePrivateRows(nil, rowVersions(4, 40), true), 4, 40)

	single := MergePrivateRows(old, rowVersions(2, 21), true)
	sameRows(t, "single replacement", single, 1, 10, 2, 21, 3, 30)
	sameRows(t, "retained after single", old, 1, 10, 2, 20, 3, 30)

	several := MergePrivateRows(old, rowVersions(3, 31, 5, 50, 1, 11), true)
	sameRows(t, "several", several, 1, 11, 2, 20, 3, 31, 5, 50)
	sameRows(t, "retained after several", old, 1, 10, 2, 20, 3, 30)

	sameRows(t, "insert-only", MergePrivateRows(old[:1:1], rowVersions(9, 90), false), 1, 10, 9, 90)
}

func TestWriteSetMergesPrepareOwnWrites(t *testing.T) {
	a, b := ids.ObjectAddress{ClassID: 1259, ObjectID: 1}, ids.ObjectAddress{ClassID: 1259, ObjectID: 2}
	previous := map[ids.ObjectAddress][]storage.RowVersion{a: rowVersions(1, 10)}
	if next := PrepareOwnWrites(previous, nil, false); len(next) != 1 || &next[a][0] != &previous[a][0] {
		t.Fatal("an empty stage must return the previous set")
	}

	spare := make([]storage.RowVersion, 1, 4)
	spare[0] = storage.RowVersion{Row: 1, Version: 10}
	previous = map[ids.ObjectAddress][]storage.RowVersion{a: spare}
	next := PrepareOwnWrites(previous, map[ids.ObjectAddress][]storage.RowVersion{a: rowVersions(2, 20), b: rowVersions(3, 30)}, false)
	if len(previous) != 1 || len(previous[a]) != 1 {
		t.Fatal("the previous set was mutated")
	}
	if len(next[a]) != 2 || &next[a][0] != &spare[0] {
		t.Fatal("an insert-only stage must extend the spare capacity of the earlier vector")
	}
	sameRows(t, "new relation", next[b], 3, 30)

	full := rowVersions(1, 10)
	previous = map[ids.ObjectAddress][]storage.RowVersion{a: full}
	next = PrepareOwnWrites(previous, map[ids.ObjectAddress][]storage.RowVersion{a: rowVersions(2, 20)}, false)
	if len(next[a]) != 2 || cap(next[a]) < 2 || &next[a][0] == &full[0] || next[a][0].Version != 10 {
		t.Fatal("a full vector must be copied into a larger one")
	}

	next = PrepareOwnWrites(previous, map[ids.ObjectAddress][]storage.RowVersion{a: rowVersions(1, 11)}, true)
	sameRows(t, "replacement", next[a], 1, 11)
	sameRows(t, "retained after replacement", full, 1, 10)
}

func TestWriteSetMergesDeletions(t *testing.T) {
	a, b := ids.ObjectAddress{ClassID: 1259, ObjectID: 1}, ids.ObjectAddress{ClassID: 1259, ObjectID: 2}
	earlier := &PrivateDeletes{Rows: map[ids.RowID]DeleteCandidate{1: {Row: 1, Version: 10}}}
	previous := map[ids.ObjectAddress]*PrivateDeletes{a: earlier}
	if next := MergeDeletions(previous, nil); len(next) != 1 || next[a] != earlier {
		t.Fatal("an empty stage must return the previous set")
	}
	staged := map[ids.ObjectAddress]*PrivateDeletes{
		a: {Rows: map[ids.RowID]DeleteCandidate{1: {Row: 1, Version: 11}, 2: {Row: 2, Version: 20}}},
		b: {Rows: map[ids.RowID]DeleteCandidate{3: {Row: 3, Version: 30}}},
	}
	next := MergeDeletions(previous, staged)
	if len(earlier.Rows) != 1 || earlier.Rows[1].Version != 10 || len(previous) != 1 {
		t.Fatal("the earlier overlay was mutated")
	}
	if got := next[a].Rows; len(got) != 2 || got[1].Version != 11 || got[2].Version != 20 {
		t.Fatalf("merged overlay %v", got)
	}
	if next[b] != staged[b] {
		t.Fatal("a new relation must take its staged overlay")
	}
}
