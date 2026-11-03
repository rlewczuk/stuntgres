package enginetests

import (
	"context"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestPgsurgeryKillStorage(t *testing.T) {
	payload := make([]sqltype.Value, 1)
	old := storage.RowVersion{Row: 7, Version: 11, Born: 1, InsertFullXID: 31, Values: payload}
	head := storage.RowVersion{Row: 7, Version: 12, Born: 2, InsertFullXID: 32, Values: payload}
	before, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{old})
	if err != nil {
		t.Fatal(err)
	}
	before = storage.CommitHeapEnds(before, []storage.RowVersion{before.Apply(old)}, 40, 3)
	original := before.Header(old.Version)
	killed := storage.KillHeapHeaders(before, []storage.RowVersion{old, old})
	want := original
	want.Slot = storage.HeapSlotDead
	if killed == before || killed.Header(old.Version) != want || before.Header(old.Version) != original || killed.Stamp != before.Stamp || &killed.Additional[0] != &before.Additional[0] {
		t.Fatal("kill changed unrelated header authority or old immutable backing")
	}
	if storage.KillHeapHeaders(killed, nil) != killed || storage.KillHeapHeaders(killed, []storage.RowVersion{old}) != killed {
		t.Fatal("no-op kill allocated")
	}
	row := killed.Apply(old)
	if !row.HeapKilled || !row.ForcedFrozen || row.HeapXmax() != 40 || row.ForcedEnd != 3 || &row.Values[0] != &payload[0] || before.Apply(old).HeapKilled || killed.Apply(head).HeapKilled {
		t.Fatal("exact death projection changed payload, successor or old capture")
	}
	for _, v := range []storage.RowVersion{row, {Row: 8, Version: 20, HeapKilled: true}, {Row: 9, Version: 21, Born: 1, ImportFrozen: true, HeapKilled: true}} {
		if visibility.LiveVersion(v, 2, nil, nil) {
			t.Fatal("death lost to liveness shortcut", v)
		}
	}
	if next, err := storage.RepairHeapHeaders(killed, []storage.RowVersion{old}); err != nil || next != killed {
		t.Fatal("freeze resurrected dead slot", next, err)
	}
	for _, next := range []*storage.HeapHeaderRoot{storage.EndHeapHeaders(killed, []storage.RowVersion{row}, 50), storage.CommitHeapEnds(killed, []storage.RowVersion{row}, 50, 4)} {
		if !next.Apply(old).HeapKilled || killed.Header(old.Version) != want {
			t.Fatal("DML publication lost death or mutated capture")
		}
	}
	source := &exec.Source{Committed: []storage.RowVersion{head}, Headers: killed, Repaired: killed.Additional}
	selected, err := exec.SelectQuerySource(context.Background(), source, 2, nil, nil, visibility.HeapOperations{})
	if err != nil || selected.Count() != 1 || selected.VersionAt(selected.RawIndex(0)).Version != head.Version {
		t.Fatal("repaired dead sibling survived", selected, err)
	}
	store := &storage.Relation{Versions: []storage.RowVersion{old, head}, Headers: killed}
	rows := storage.VisibleHeapVersions(store, 2)
	if len(rows) != 1 || rows[0].Version != head.Version || &rows[0].Values[0] != &payload[0] {
		t.Fatal("dump projection exported dead version", rows)
	}
	mapped, err := storage.MapHeapHeaderRows(killed, func(v storage.RowVersion) (storage.RowVersion, bool, error) {
		v.Values = make([]sqltype.Value, 2)
		return v, true, nil
	})
	if err != nil || !mapped.Apply(old).HeapKilled || mapped.Header(old.Version) != want || len(killed.Additional[0].Values) != 1 {
		t.Fatal("shape transport lost death or mutated old payload", err)
	}
	fresh := &storage.Relation{Versions: []storage.RowVersion{old}}
	if fresh.Headers.Apply(old).HeapKilled {
		t.Fatal("fresh incarnation inherited stale authority")
	}
}

func TestPgsurgeryKillMaintenance(t *testing.T) {
	payload := make([]sqltype.Value, 1)
	dead := storage.RowVersion{Row: 1, Version: 1, Born: 1, InsertFullXID: 3, Values: payload}
	live := storage.RowVersion{Row: 2, Version: 2, Born: 1, InsertFullXID: 4, Values: payload}
	headers := storage.KillHeapHeaders(nil, []storage.RowVersion{dead})
	source := storage.Relation{Versions: []storage.RowVersion{dead, live}, Headers: headers}
	rows := storage.EffectiveHeapRows(source.Versions, headers)
	if &rows[0] == &source.Versions[0] || !rows[0].HeapKilled || source.Versions[0].HeapKilled {
		t.Fatal("effective rows borrowed mutable death projection")
	}
	candidate, err := storage.InspectVacuum(t.Context(), rows, nil, 2, 3, true, storage.VacuumProgress{})
	if err != nil || candidate.Live != 1 || candidate.Removed != 1 {
		t.Fatal("vacuum counted killed tuple as live", candidate, err)
	}
	candidate, err = storage.MaterializeVacuum(t.Context(), rows, nil, 2, 3, candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err = storage.MaintainHeapHistory(t.Context(), candidate, source, 2, true, false, false)
	if err != nil || len(candidate.Versions) != 1 || candidate.Versions[0].Version != live.Version || candidate.Headers.Header(dead.Version).Slot != storage.HeapSlotUnused || !candidate.Headers.Apply(dead).HeapKilled || headers.Header(dead.Version).Slot != storage.HeapSlotDead {
		t.Fatal("retirement resurrected payload or mutated old root", candidate, err)
	}
	fact := storage.HeapTupleFact{Version: headers.Apply(dead)}
	if storage.HeapTupleLive(fact, ids.TxRef{}, false) || storage.HeapTupleLive(fact, ids.TxRef{}, true) || storage.VisibilityTupleBits(fact, storage.VisibilityHorizon{}, false).AllVisible {
		t.Fatal("statistics/VM treated dead slot as live")
	}
	if fresh, err := storage.RepairHeapHeaders(candidate.Headers, []storage.RowVersion{dead}); err != nil || fresh != candidate.Headers {
		t.Fatal("freeze revived retired payload", fresh, err)
	}
	selected := []storage.RowVersion{dead}
	if allocations := testing.AllocsPerRun(100, func() {
		if storage.KillHeapHeaders(headers, selected) != headers {
			t.Fatal("dead no-op publication")
		}
	}); allocations != 0 {
		t.Fatal("dead no-op allocated", allocations)
	}
}
