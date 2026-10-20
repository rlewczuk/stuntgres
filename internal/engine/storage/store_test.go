package storage

import (
	"math"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestStoreAllocateRowVersionsInOrder(t *testing.T) {
	var s Store
	s.SetFrontierLocked(10, 20)
	admission := catalog.NewState(1)
	rows := [][]sqltype.Value{{{}}, {{}}}
	versions, err := s.AllocateRowVersionsLocked(rows, admission)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}
	for i, want := range []struct {
		row     ids.RowID
		version ids.VersionID
	}{{11, 21}, {12, 22}} {
		v := versions[i]
		if v.Row != want.row || v.Version != want.version || v.Born != 0 || v.Base != 0 || v.IndexAdmission != admission || &v.Values[0] != &rows[i][0] {
			t.Fatalf("version %d = %+v, want row %d version %d sharing the input row", i, v, want.row, want.version)
		}
	}
	if s.RowFrontier() != 12 || s.VersionFrontier() != 22 {
		t.Fatalf("frontier = %d/%d, want 12/22", s.RowFrontier(), s.VersionFrontier())
	}
}

func TestStoreAllocateRowVersionsExhaustion(t *testing.T) {
	rows := [][]sqltype.Value{{{}}, {{}}}
	for _, tc := range []struct {
		name         string
		row, version uint64
		message      string
	}{
		{"row", math.MaxUint64 - 1, 0, "stuntgres: row identity exhausted"},
		{"version", 0, math.MaxUint64 - 1, "stuntgres: version identity exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s Store
			s.SetFrontierLocked(tc.row, tc.version)
			versions, err := s.AllocateRowVersionsLocked(rows, nil)
			if err == nil || err.Error() != tc.message || versions != nil {
				t.Fatalf("got %v, %v; want %q", versions, err, tc.message)
			}
			if s.RowFrontier() != tc.row || s.VersionFrontier() != tc.version {
				t.Fatalf("frontier moved to %d/%d", s.RowFrontier(), s.VersionFrontier())
			}
		})
	}
	var s Store
	s.SetFrontierLocked(math.MaxUint64-2, math.MaxUint64-2)
	if versions, err := s.AllocateRowVersionsLocked(rows, nil); err != nil || versions[1].Row != math.MaxUint64 || versions[1].Version != math.MaxUint64 {
		t.Fatalf("allocation up to the last identity = %v, %v", versions, err)
	}
}

func TestStoreSingleAllocatorsAdvanceIndependently(t *testing.T) {
	var s Store
	s.SetFrontierLocked(5, 9)
	if v := s.AllocateVersionLocked(); v != 10 {
		t.Fatalf("version = %d, want 10", v)
	}
	if s.RowFrontier() != 5 {
		t.Fatalf("version allocation moved the row frontier to %d", s.RowFrontier())
	}
	if r := s.AllocateRowLocked(); r != 6 {
		t.Fatalf("row = %d, want 6", r)
	}
	if s.RowFrontier() != 6 || s.VersionFrontier() != 10 {
		t.Fatalf("frontier = %d/%d, want 6/10", s.RowFrontier(), s.VersionFrontier())
	}
}

func TestStoreIndexRootsCurrent(t *testing.T) {
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1}
	b := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2}
	root, other := &IndexRoot{}, &IndexRoot{}
	var s Store
	s.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*IndexRoot{a: root})
	for _, tc := range []struct {
		name     string
		captured map[ids.ObjectAddress]*IndexRoot
		want     bool
	}{
		{"none", nil, true},
		{"same", map[ids.ObjectAddress]*IndexRoot{a: root}, true},
		{"replaced", map[ids.ObjectAddress]*IndexRoot{a: other}, false},
		{"absent", map[ids.ObjectAddress]*IndexRoot{b: root}, false},
		{"absent nil", map[ids.ObjectAddress]*IndexRoot{b: nil}, true},
	} {
		if got := s.IndexRootsCurrent(tc.captured); got != tc.want {
			t.Errorf("%s: IndexRootsCurrent = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRelationIndexIdentity(t *testing.T) {
	origin := &Relation{}
	capture := &Relation{IndexSource: origin}
	if origin.IndexIdentity() != origin || capture.IndexIdentity() != origin {
		t.Fatal("index identity must be the build origin, else the storage itself")
	}
}

func TestRelationSameTruncateLineage(t *testing.T) {
	token := &TruncateLineage{}
	first, second := &Relation{TruncateLineage: token}, &Relation{TruncateLineage: token}
	other := &Relation{TruncateLineage: &TruncateLineage{}}
	plain := &Relation{}
	var missing *Relation
	for _, tc := range []struct {
		name string
		a, b *Relation
		want bool
	}{
		{"shared token", first, second, true},
		{"distinct tokens", first, other, false},
		{"no token", plain, plain, false},
		{"nil receiver", missing, first, false},
		{"nil argument", first, missing, false},
	} {
		if got := tc.a.SameTruncateLineage(tc.b); got != tc.want {
			t.Errorf("%s: SameTruncateLineage = %v, want %v", tc.name, got, tc.want)
		}
	}
}
