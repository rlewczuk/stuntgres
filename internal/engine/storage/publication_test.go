package storage

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestStampPublishedBirth(t *testing.T) {
	r := &Relation{Versions: []RowVersion{{Born: 1, InsertFullXID: 3, Frozen: true}, {ImportFrozen: true}}}
	r.Maintenance.FrozenXID = 20
	r.StampPublishedBirth(7, 9)
	for i, v := range r.Versions {
		if v.Born != 7 || v.InsertFullXID != 9 || v.Frozen || v.ImportFrozen {
			t.Fatalf("version %d = %+v", i, v)
		}
	}
	if r.Maintenance.FrozenXID != 9 {
		t.Fatalf("frozen bound = %d, want the birth 9", r.Maintenance.FrozenXID)
	}
	r.CountPublishedInserts()
	m := r.Maintenance
	if m.Inserts != 2 || m.InsertsSince != 2 || m.Modifications != 2 || m.Live != 2 || m.Dead != 0 {
		t.Fatalf("maintenance = %+v", m)
	}
}

func TestReplacementCapacityLocked(t *testing.T) {
	var s Store
	relations := map[ids.ObjectAddress]*Relation{{ObjectID: 1}: {Versions: make([]RowVersion, 2)}}
	if err := s.ReplacementCapacityLocked(relations, 1, 1); err != nil {
		t.Fatal(err)
	}
	max := ^uint64(0)
	cases := []struct {
		row, version uint64
		epoch        ids.CatalogEpoch
		commit       ids.CommitSeq
		want         string
	}{
		{0, 0, ids.CatalogEpoch(max), ids.CommitSeq(max), "stuntgres: catalog epoch exhausted"},
		{max - 1, max - 1, 1, ids.CommitSeq(max), "stuntgres: row identity exhausted"},
		{0, max - 1, 1, ids.CommitSeq(max), "stuntgres: version identity exhausted"},
		{0, 0, 1, ids.CommitSeq(max), "stuntgres: commit sequence exhausted"},
	}
	for _, tc := range cases {
		s.SetFrontierLocked(tc.row, tc.version)
		if err := s.ReplacementCapacityLocked(relations, tc.epoch, tc.commit); err == nil || err.Error() != tc.want {
			t.Fatalf("capacity error = %v, want %q", err, tc.want)
		}
	}
}

func TestPreserveUnchangedRelations(t *testing.T) {
	a, b, removed, created := ids.ObjectAddress{ObjectID: 1}, ids.ObjectAddress{ObjectID: 2}, ids.ObjectAddress{ObjectID: 3}, ids.ObjectAddress{ObjectID: 4}
	oldA, oldB, oldRemoved := &Relation{}, &Relation{}, &Relation{}
	liveA, liveB := &Relation{}, &Relation{}
	replacement, initial := &Relation{}, &Relation{}
	previous := map[ids.ObjectAddress]*Relation{a: oldA, b: oldB, removed: oldRemoved}
	live := map[ids.ObjectAddress]*Relation{a: liveA, b: liveB, removed: oldRemoved}
	prepared := map[ids.ObjectAddress]*Relation{a: oldA, b: replacement, created: initial}
	PreserveUnchangedRelations(prepared, previous, live)
	if len(prepared) != 3 || prepared[a] != liveA || prepared[b] != replacement || prepared[created] != initial || prepared[removed] != nil {
		t.Fatal("reconciliation replaced private stores or resurrected a removal")
	}
	if previous[a] != oldA || previous[b] != oldB || live[a] != liveA || live[b] != liveB || live[removed] != oldRemoved {
		t.Fatal("reconciliation mutated captured or published maps")
	}
}
