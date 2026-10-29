package enginetests_test

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGProcProjection(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	state := catalog.CloneState(base.State())
	declaration := *base.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1397})
	declaration.SecurityDefiner = true
	declaration.Parallel = 'r'
	declaration.Cost = 7
	declaration.Configuration = catalog.NewRoutineConfiguration([]string{"work_mem=64kB", "search_path=public, pg_catalog"})
	state.SetDescriptor(declaration.Address(), &declaration)
	view := e.Catalog.Read(state)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ProcedureClassID})
	want := []int32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30}
	if len(source.Columns) != 30 || source.ColumnMeta.HighWater != 30 {
		t.Fatal("catalog width/high water", source)
	}
	for i, attribute := range want {
		if source.Attributes[i] != attribute || source.Columns[i].OriginAttribute != int16(attribute) {
			t.Fatal("released attribute identity", i, source.Attributes[i])
		}
	}
	if source.Columns[10].Usage.Type.ID() != sqltype.Bool || source.Columns[10].Nullable || source.Columns[28].Usage.Type.ID() != 1009 || !source.Columns[28].Nullable || source.ColumnMeta.CatalogAttributes[28].NotNull || source.ColumnMeta.CatalogAttributes[28].Dimensions != 1 || source.ColumnMeta.CatalogAttributes[28].Collation != 950 {
		t.Fatal("configuration metadata")
	}
	produce, ok := pgcatalog.ProducerFor(ids.ProcedureClassID)
	if !ok {
		t.Fatal("missing producer")
	}
	rows, err := produce(pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.PublishedRead(view.Owner(), view), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}})
	if err != nil {
		t.Fatal(err)
	}
	seenMain, seenInternal := false, false
	for _, row := range rows {
		if len(row) != 30 || row[10].Type() != source.Columns[10].Usage.Type || row[10].IsNull() || row[28].Type() != source.Columns[28].Usage.Type {
			t.Fatal("descriptor projection width/types")
		}
		id, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if id == 1397 {
			seenMain = true
			security, err := row[10].Boolean()
			if err != nil || !security {
				t.Fatal("canonical security", err)
			}
			config, err := row[28].Type().Output(t.Context(), row[28])
			if err != nil || config != "{work_mem=64kB,\"search_path=public, pg_catalog\"}" || row[28].ArrayDimension(0).Lower != 1 {
				t.Fatal("ordered owned config", config, err)
			}
			cost, err := row[5].Float()
			if err != nil || cost != 7 {
				t.Fatal("canonical cost", cost, err)
			}
			parallel, err := row[15].Text()
			if err != nil || parallel != "r" {
				t.Fatal("canonical parallel", parallel, err)
			}
		}
		if id == 40 {
			seenInternal = true
			security, err := row[10].Boolean()
			if err != nil || security || !row[28].IsNull() {
				t.Fatal("internal descriptor security/config", err)
			}
		}
	}
	if !seenMain || !seenInternal {
		t.Fatal("missing descriptor witnesses")
	}
	internal := base.MustProcedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:40})
	if internal.Builtin == nil || internal.Builtin.Providers != 0 || internal.Display.Source != "int2vectorin" {
		t.Fatal("internal metadata must have a genuine non-executable descriptor")
	}
	if base.MustProcedure(declaration.Address()).Configuration != nil {
		t.Fatal("capture changed retained base")
	}
}
