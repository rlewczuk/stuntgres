package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestPgsurgeryFreezeRowPublication(t *testing.T) {
	domain := new(ids.Domain)
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384}
	ref := domain.Ref(1, address)
	tx := domain.Tx(domain.Session(1), 1, 1, nil, 40)
	old := storage.RowVersion{Row: 7, Version: 10, Born: 1, InsertFullXID: 30}
	sibling := storage.RowVersion{Row: 7, Version: 20, Born: 2, InsertFullXID: 31}
	headers, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{old})
	if err != nil {
		t.Fatal(err)
	}
	frozen := headers.Apply(old)
	prepare := func(root, writes []storage.RowVersion, h *storage.HeapHeaderRoot, deletions *visibility.PrivateDeletes) index.RowPublication {
		t.Helper()
		store := &storage.Relation{Versions: root, Headers: h}
		if deletions != nil {
			copy := deletions.Clone()
			for _, candidate := range deletions.All() {
				candidate.Storage = store
				copy.Set(candidate)
			}
			deletions = copy
		}
		publication := index.RowPublication{Address: address, Store: store, Relation: &catalog.Relation{}, Root: root, Writes: writes, Headers: h, FullXID: 40, Deletion: deletions, SingleSlot: -1}
		if err := index.PrepareRowPublication(&publication, domain, tx, true); err != nil {
			t.Fatal(err)
		}
		return publication
	}
	t.Run("pre_repair_writer_keeps_restored_predecessor", func(t *testing.T) {
		pending := sibling
		pending.Born, pending.Base = 0, old.Version
		pending.PrivateHistory = storage.RetainHeapPredecessor(old)
		publication := prepare([]storage.RowVersion{old}, []storage.RowVersion{pending}, headers, nil)
		if len(publication.Next) != 2 || publication.SingleSlot != 1 || publication.Next[0].Version != old.Version || publication.NextHeaders != headers || publication.NextRetained != nil {
			t.Fatal("old admission overwrote restored slot/header", publication)
		}
	})
	t.Run("genuine_repaired_base_replaces_only_its_slot", func(t *testing.T) {
		next := storage.RowVersion{Row: old.Row, Version: 30, Base: old.Version, InsertFullXID: 40, PrivateHistory: storage.RetainHeapPredecessor(frozen)}
		raw := storage.ReplaceHeapHeaders(headers, []storage.RowVersion{next}, 40)
		publication := prepare([]storage.RowVersion{old, sibling}, []storage.RowVersion{next}, raw, nil)
		if len(publication.Next) != 2 || publication.SingleSlot != 0 || publication.Next[1].Version != sibling.Version || publication.NextRetained != nil {
			t.Fatal("replacement collapsed an unrelated physical sibling", publication)
		}
		if publication.NextHeaders == raw || publication.NextHeaders.Header(old.Version).Successor != next.Version || publication.NextHeaders.Header(old.Version).Xmax != 40 || raw.Header(old.Version).End != 0 {
			t.Fatal("accepted successor/header capture", publication.NextHeaders, raw)
		}
	})
	t.Run("retained_base_appends_without_retargeting_head", func(t *testing.T) {
		next := storage.RowVersion{Row: old.Row, Version: 30, Base: old.Version, InsertFullXID: 40, PrivateHistory: storage.RetainHeapPredecessor(frozen)}
		raw := storage.ReplaceHeapHeaders(headers, []storage.RowVersion{next}, 40)
		publication := prepare([]storage.RowVersion{sibling}, []storage.RowVersion{next}, raw, nil)
		if len(publication.Next) != 2 || publication.SingleSlot != 1 || publication.Next[0].Version != sibling.Version || len(publication.Ended) != 1 || publication.Ended[0].RepairStamp != frozen.RepairStamp {
			t.Fatal("retained-base publication lost exact version", publication)
		}
	})
	t.Run("both_current_sibling_deletions_survive", func(t *testing.T) {
		deletions := &visibility.PrivateDeletes{}
		deletions.Set(visibility.DeleteCandidate{Ref: ref, Row: old.Row, Version: old.Version, RepairStamp: frozen.RepairStamp, EndFullXID: 40})
		deletions.Set(visibility.DeleteCandidate{Ref: ref, Row: sibling.Row, Version: sibling.Version, EndFullXID: 40})
		publication := prepare([]storage.RowVersion{old, sibling}, nil, headers, deletions)
		if publication.NextEnds.Len() != 2 || len(publication.Ended) != 2 {
			t.Fatal("same-RowID deletion collapsed", publication.NextEnds, publication.Ended)
		}
		for _, version := range []ids.VersionID{old.Version, sibling.Version} {
			if marker, ok := publication.NextEnds.Lookup(old.Row, version); !ok || marker.Version != version {
				t.Fatal("missing exact current-head end", version, marker)
			}
		}
	})
	t.Run("old_writer_cannot_reend_later_repaired_mutation", func(t *testing.T) {
		next := storage.RowVersion{Row: old.Row, Version: 30, Base: old.Version, InsertFullXID: 50, PrivateHistory: storage.RetainHeapPredecessor(frozen)}
		raw := storage.ReplaceHeapHeaders(headers, []storage.RowVersion{next}, 50)
		committed := storage.CommitHeapEnds(raw, []storage.RowVersion{frozen}, 50, 3)
		next.Born, next.Base, next.PrivateHistory = 3, 0, nil
		pending := sibling
		pending.Born, pending.Base = 0, old.Version
		pending.PrivateHistory = storage.RetainHeapPredecessor(old)
		publication := prepare([]storage.RowVersion{next}, []storage.RowVersion{pending}, committed, nil)
		if len(publication.Next) != 2 || publication.SingleSlot != 1 || publication.Next[0].Version != next.Version || publication.NextHeaders != committed || committed.Header(old.Version).End != 3 || committed.Header(old.Version).Xmax != 50 {
			t.Fatal("pre-repair UPDATE rejected or clobbered later DML", publication)
		}
		deletions := &visibility.PrivateDeletes{}
		deletions.Set(visibility.DeleteCandidate{Ref: ref, Row: old.Row, Version: old.Version, EndFullXID: 40})
		publication = prepare([]storage.RowVersion{next}, nil, committed, deletions)
		if publication.NextEnds != nil || publication.NextHeaders != committed {
			t.Fatal("pre-repair DELETE reintroduced cleared end", publication)
		}
	})
}
