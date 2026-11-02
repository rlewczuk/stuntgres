package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
)

func TestPgsurgeryFreezeMaintenanceFences(t *testing.T) {
	row := storage.RowVersion{Row: 1, Version: 1, Born: 1, InsertFullXID: 3}
	rows := []storage.RowVersion{row}
	capture := table.VacuumCapture{Rows: rows, Generation: 4, Frontier: 1, Cutoff: 3, Relation: &catalog.Relation{}}
	stores := &storage.Store{}
	if !capture.SameLocked(capture, stores, false) {
		t.Fatal("unchanged capture rejected")
	}
	headers, err := storage.RepairHeapHeaders(nil, rows)
	if err != nil {
		t.Fatal(err)
	}
	current := capture
	current.Headers = headers
	if capture.SameLocked(current, stores, false) {
		t.Fatal("changed exact header root accepted")
	}
	current = capture
	current.Discarded, _ = storage.RetainDiscardedVersions(nil, []storage.RowVersion{{Row: 2, Version: 2}}, nil)
	if capture.SameLocked(current, stores, false) {
		t.Fatal("changed discarded root accepted")
	}
	capture.Headers = headers
	store := &storage.Relation{Versions: rows, Headers: headers, Generation: 4}
	pass, err := table.PrepareVacuumPass(context.Background(), capture, store, func(table.VacuumProgress) {})
	if err != nil {
		t.Fatal(err)
	}
	if pass.Candidate.Live != 1 || pass.Candidate.Headers != headers || pass.Generation != 5 || !pass.Candidate.Versions[0].ForcedFrozen {
		t.Fatal("effective header publication missing", pass)
	}
	if rows[0].ForcedFrozen || store.Generation != 4 {
		t.Fatal("detached pass changed captured source")
	}
	capture.Generation = ^uint64(0)
	if _, err := table.PrepareVacuumPass(context.Background(), capture, store, func(table.VacuumProgress) {}); err == nil {
		t.Fatal("changed-root generation overflow accepted")
	}
	plain := table.VacuumCapture{Rows: rows, Generation: 4, Frontier: 1, Cutoff: 3, Relation: &catalog.Relation{}}
	if allocs := testing.AllocsPerRun(20, func() {
		mapped := storage.EffectiveHeapRows(rows, nil)
		if !storage.SameVersionRoot(mapped, rows) {
			panic("no-op header selection copied rows")
		}
		visible := storage.VisibleHeapVersions(&storage.Relation{Versions: rows}, 1)
		if !storage.SameVersionRoot(visible, rows) {
			panic("clean visible capture copied rows")
		}
	}); allocs != 0 {
		t.Fatal("no-op exact header selection allocated", allocs)
	}
	pass, err = table.PrepareVacuumPass(context.Background(), plain, &storage.Relation{Versions: rows}, func(table.VacuumProgress) {})
	if err != nil || pass.Generation != 4 || !storage.SameVersionRoot(pass.Candidate.Versions, rows) {
		t.Fatal("no-op pass copied roots", pass, err)
	}
}
