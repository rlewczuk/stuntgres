package engine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type columnsUnitFixture struct {
	dc    dcUnitFixture
	state *catalog.State
	table *catalog.Relation
}

func (f columnsUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.dc.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.dc.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func newColumnsUnitFixture(t *testing.T) columnsUnitFixture {
	t.Helper()
	dc := newDCUnitFixture(t)
	state := catalog.CloneState(dc.state)
	view := dc.f.e.Catalog.Read(state)
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	nested, _ := view.LookupType(22005)
	literal, _ := sqltype.Integer(integer, 7)
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22400}, ObjectNamespace: dc.f.schema, ObjectName: "columns_unit", ObjectRevision: 1, Owner: dc.f.owner, RelationKind: catalog.TableRelation,
		Columns: []catalog.Column{{Name: "a", Usage: integer.Usage(), Nullable: false, NullabilityKnown: true}, {Name: "b", Usage: boolean.Usage(), Nullable: true, NullabilityKnown: true}, {Name: "nested", Usage: nested.Usage(), Nullable: true, NullabilityKnown: true}}, Attributes: []int32{1, 3, 4}, Index: map[string]int{"a": 0, "b": 1, "nested": 2},
		ColumnMeta: catalog.ColumnMetadata{HighWater: 4, Defaults: map[int32]catalog.ColumnDefault{1: {OID: 22500, Expression: plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: literal}}}}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}}
	state.SetDescriptor(table.Address(), table)
	return columnsUnitFixture{dc: dc, state: state, table: table}
}

func columnsUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoColumnsOID)
	if !ok {
		t.Fatal("missing columns producer")
	}
	source, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Ref != in.Ref || source.View != nil || source.Anonymous == nil {
		t.Fatal("source authority/empty contract", source)
	}
	for _, row := range source.Anonymous {
		if len(row) != 44 {
			t.Fatal("columns width", len(row))
		}
		for i, value := range row {
			if value.Type() != in.Source.Columns[i].Usage.Type || !value.ValidResult(in.Source.Columns[i].Usage) {
				t.Fatal("columns cell authority", i, value)
			}
		}
	}
	return source.Anonymous
}

func columnsUnitCell(t *testing.T, value sqltype.Value) any {
	t.Helper()
	if value.IsNull() {
		return nil
	}
	base, err := value.BaseValue()
	if err != nil {
		t.Fatal(err)
	}
	if base.Type().ID() == sqltype.Int4 {
		n, err := base.Integer()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	text, err := base.Text()
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func columnsUnitTable(t *testing.T, rows [][]sqltype.Value) map[string][44]any {
	t.Helper()
	out := map[string][44]any{}
	for _, row := range rows {
		if columnsUnitCell(t, row[2]) != "columns_unit" {
			continue
		}
		var cells [44]any
		for i, value := range row {
			cells[i] = columnsUnitCell(t, value)
		}
		name := cells[3].(string)
		if _, exists := out[name]; exists {
			t.Fatal("duplicate column", name)
		}
		out[name] = cells
	}
	return out
}

func columnsUnitExpected() map[string][44]any {
	base := func(name string, ordinal int64, dataType, udtSchema, udt string) [44]any {
		var row [44]any
		row[0], row[1], row[2], row[3], row[4] = "captured_database", "default_schema", "columns_unit", name, ordinal
		row[6], row[7] = "YES", dataType
		row[25], row[26], row[27] = "captured_database", udtSchema, udt
		row[33], row[34], row[40], row[41], row[43] = "NO", "NO", "NO", "NEVER", "YES"
		return row
	}
	a := base("a", 1, "integer", "pg_catalog", "int4")
	a[5], a[6], a[10], a[11], a[12], a[32] = "7", "NO", int64(32), int64(2), int64(0), "1"
	b := base("b", 3, "boolean", "pg_catalog", "bool")
	b[32] = "3"
	nested := base("nested", 4, "USER-DEFINED", "default_schema", "nn")
	nested[22], nested[23], nested[24], nested[32] = "captured_database", "default_schema", "nn_child", "4"
	return map[string][44]any{"a": a, "b": b, "nested": nested}
}
