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

func TestRoutineAttributeCatalogProjection(t *testing.T) {
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
	want := []int32{1, 2, 3, 4, 17, 19, 15, 13, 16, 10, 23, 5, 14, 7, 26, 20, 8, 21, 22, 30, 18, 6, 11, 29}
	if len(source.Columns) != 24 || source.ColumnMeta.HighWater != 30 {
		t.Fatal("catalog width/high water", source)
	}
	for i, attribute := range want {
		if source.Attributes[i] != attribute || source.Columns[i].OriginAttribute != int16(attribute) {
			t.Fatal("released attribute identity", i, source.Attributes[i])
		}
	}
	if source.Columns[22].Usage.Type.ID() != sqltype.Bool || source.Columns[22].Nullable || source.Columns[23].Usage.Type.ID() != 1009 || !source.Columns[23].Nullable || source.ColumnMeta.CatalogAttributes[23].NotNull || source.ColumnMeta.CatalogAttributes[23].Dimensions != 1 || source.ColumnMeta.CatalogAttributes[23].Collation != 950 {
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
	seenMain, seenSupplement := false, false
	for _, row := range rows {
		if len(row) != 24 || row[22].Type() != source.Columns[22].Usage.Type || row[22].IsNull() || row[23].Type() != source.Columns[23].Usage.Type {
			t.Fatal("both producer widths/types")
		}
		id, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if id == 1397 {
			seenMain = true
			security, err := row[22].Boolean()
			if err != nil || !security {
				t.Fatal("canonical security", err)
			}
			config, err := row[23].Type().Output(t.Context(), row[23])
			if err != nil || config != "{work_mem=64kB,\"search_path=public, pg_catalog\"}" || row[23].ArrayDimension(0).Lower != 1 {
				t.Fatal("ordered owned config", config, err)
			}
			cost, err := row[21].Float()
			if err != nil || cost != 7 {
				t.Fatal("canonical cost", cost, err)
			}
			parallel, err := row[8].Text()
			if err != nil || parallel != "r" {
				t.Fatal("canonical parallel", parallel, err)
			}
		}
		if id == 40 {
			seenSupplement = true
			security, err := row[22].Boolean()
			if err != nil || security || !row[23].IsNull() {
				t.Fatal("supplement security/config", err)
			}
		}
	}
	if !seenMain || !seenSupplement {
		t.Fatal("missing producer witnesses")
	}
	if base.MustProcedure(declaration.Address()).Configuration != nil {
		t.Fatal("capture changed retained base")
	}
}
