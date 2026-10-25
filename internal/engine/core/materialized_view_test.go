package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestMaterializedViewInitialStore(t *testing.T) {
	e := engine.New()
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
	store := core.StageInitialTables(stage)[d.Address()]
	if &store.Versions[0].Values[0] != &rows[0][0] {
		t.Fatal("finalized row buffer copied")
	}
	if err := table.ValidateInitialTables(core.StageInitialTables(stage), stage.Get, stage.Base); err != nil {
		t.Fatal(err)
	}
	d.Populated = false
	if err := table.ValidateInitialTables(core.StageInitialTables(stage), stage.Get, stage.Base); err == nil {
		t.Fatal("unpopulated store accepted rows")
	}
	d.Populated, d.RelationKind = false, catalog.ViewRelation
	if err := table.ValidateInitialTables(core.StageInitialTables(stage), stage.Get, stage.Base); err == nil {
		t.Fatal("dynamic view accepted physical rows")
	}
}

func TestMaterializedViewPublication(t *testing.T) {
	e := engine.New()
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
	if core.StageInitialTables(stage) != nil || e.Catalog.State != before {
		t.Fatal("canceled stage published rows/catalog")
	}
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	e.Catalog.State.SetEpoch(e.Catalog.State.Epoch() + 1)
	if err := e.StageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	if core.StageInitialTables(stage) != nil {
		t.Fatal("stale stage retained rows")
	}
}

// Exercise the private binder/stager directly, not SQL execution wrappers.
func materializedUnitStage(t *testing.T, e *core.Engine) (*session.Operation, func(), *core.CatalogStage, *catalog.Relation) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	statements, err := parser.Parse("CREATE MATERIALIZED VIEW public.unit_mv AS SELECT 1::int AS i WITH NO DATA")
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*parser.CreateQueryTable)
	input, _ := op.Session.Settings.Presentation()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b, err := core.Describe(context.Background(), e.AnalysisHost(op), s, auth, ids.ObjectRef{}, input, nil, nil)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage, descriptor, skipped, err := core.EnginePrepareQueryTableLocked(e, op, auth, s, b)
	e.Mu.Unlock()
	if err != nil || skipped {
		t.Fatalf("private stage: %v, skipped %v", err, skipped)
	}
	return op, done, stage, descriptor
}
