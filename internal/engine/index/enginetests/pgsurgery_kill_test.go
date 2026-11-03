package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestPgsurgeryKillRowPublication(t *testing.T) {
	domain := new(ids.Domain)
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384}
	tx := domain.Tx(domain.Session(1), 1, 1, nil, 40)
	old := storage.RowVersion{Row: 7, Version: 10, Born: 1, InsertFullXID: 30}
	pending := storage.RowVersion{Row: old.Row, Version: 20, Base: old.Version, InsertFullXID: 40, PrivateHistory: storage.RetainHeapPredecessor(old)}
	for _, killedHead := range []bool{false, true} {
		selected := old
		if killedHead {
			selected = pending
		}
		headers := storage.KillHeapHeaders(nil, []storage.RowVersion{selected})
		store := &storage.Relation{Versions: []storage.RowVersion{old}, Headers: headers}
		p := index.RowPublication{Address: address, Store: store, Relation: &catalog.Relation{}, Root: store.Versions, Writes: []storage.RowVersion{pending}, Headers: headers, FullXID: 40}
		// PREPARE validates the same exact ancestry without materializing heads.
		if err := index.PrepareRowPublication(&p, domain, tx, false); err != nil {
			t.Fatal(err)
		}
		if p.Next != nil || p.NextHeaders != nil {
			t.Fatal("prepare materialized private resources")
		}
		if err := index.PrepareRowPublication(&p, domain, tx, true); err != nil {
			t.Fatal(err)
		}
		final := storage.CommitHeapEnds(p.NextHeaders, p.Ended, 40, 2)
		if final.Header(selected.Version).Slot != storage.HeapSlotDead || headers.Header(selected.Version).Slot != storage.HeapSlotDead {
			t.Fatal("final publication lost immutable death")
		}
		if final.Apply(pending).HeapKilled != killedHead {
			t.Fatal("death retargeted distinct successor")
		}
		bad := p
		bad.Writes = append([]storage.RowVersion(nil), p.Writes...)
		bad.Writes[0].Base = 999
		if err := index.PrepareRowPublication(&bad, domain, tx, false); err == nil {
			t.Fatal("kill weakened structural predecessor admission")
		}
		// A normal ended predecessor remains a conflict even when also killed.
		ended := p
		ended.Ends = &storage.DeletionRoot{}
		ended.Ends.Set(old.Row, storage.RowEnd{Row: old.Row, Version: old.Version, End: 1})
		if err := index.PrepareRowPublication(&ended, domain, tx, false); err == nil {
			t.Fatal("kill hid ordinary committed predecessor conflict")
		}
		if !killedHead {
			deletes := &visibility.PrivateDeletes{}
			deletes.Set(visibility.DeleteCandidate{Ref: domain.Ref(1, address), Storage: store, Row: old.Row, Version: old.Version})
			d := index.RowPublication{Address: address, Store: store, Relation: &catalog.Relation{}, Root: store.Versions, Headers: headers, Deletion: deletes, FullXID: 40}
			if err := index.PrepareRowPublication(&d, domain, tx, true); err != nil || !d.NextHeaders.Apply(old).HeapKilled {
				t.Fatal("admitted delete lost kill", err)
			}
		}
	}
}

func TestPgsurgeryKillUniqueAdmission(t *testing.T) {
	old := storage.RowVersion{Row: 7, Version: 10, Born: 1}
	successor := storage.RowVersion{Row: 7, Version: 11, Base: old.Version}
	headers := storage.KillHeapHeaders(nil, []storage.RowVersion{old})
	if !index.UniqueRowDeleted(headers.Apply(old)) || index.UniqueRowDeleted(old) || index.UniqueRowDeleted(headers.Apply(successor)) {
		t.Fatal("unique admission lost exact death or retargeted a successor")
	}
}
