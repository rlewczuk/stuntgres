package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestMaterializedViewInitialStore(t *testing.T) {
	e := New()
	_, done, stage, d := materializedUnitStage(t, e)
	defer done()
	d.Populated = true
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	rows := [][]sqltype.Value{{value}}
	if err := e.StageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	store := StageInitialTables(stage)[d.Address()]
	if &store.Versions[0].Values[0] != &rows[0][0] {
		t.Fatal("finalized row buffer copied")
	}
	if err := table.ValidateInitialTables(StageInitialTables(stage), stage.Get, stage.Base); err != nil {
		t.Fatal(err)
	}
	d.Populated = false
	if err := table.ValidateInitialTables(StageInitialTables(stage), stage.Get, stage.Base); err == nil {
		t.Fatal("unpopulated store accepted rows")
	}
	d.Populated, d.RelationKind = false, catalog.ViewRelation
	if err := table.ValidateInitialTables(StageInitialTables(stage), stage.Get, stage.Base); err == nil {
		t.Fatal("dynamic view accepted physical rows")
	}
}

func TestMaterializedViewPublication(t *testing.T) {
	e := New()
	_, done, stage, d := materializedUnitStage(t, e)
	defer done()
	d.Populated = true
	before := e.Catalog.State
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	rows := [][]sqltype.Value{{value}}
	if err := e.StageQueryTableRows(ctx, stage, d, rows, ids.TxRef{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if StageInitialTables(stage) != nil || e.Catalog.State != before {
		t.Fatal("canceled stage published rows/catalog")
	}
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	e.Catalog.State.SetEpoch(e.Catalog.State.Epoch() + 1)
	if err := e.StageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	if StageInitialTables(stage) != nil {
		t.Fatal("stale stage retained rows")
	}
}
