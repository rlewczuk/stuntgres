package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func tablesUnitInput(t *testing.T, f defaultUnitFixture, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.e.catalog.read(state)
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoTablesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engineCatalogRuntime{auth: auth, database: "captured_database"}}
}

func TestInfoTablesProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.catalog.state)
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22400}, ObjectNamespace: f.schema, ObjectName: "tables_unit", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: f.owner}
	state.SetDescriptor(table.Address(), table)
	in := tablesUnitInput(t, f, state, f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTablesOID)
	rows, err := produce(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"captured_database", "default_schema", "tables_unit", "BASE TABLE", nil, nil, nil, nil, nil, "YES", "NO", nil}
	var retained []sqltype.Value
	for _, row := range rows {
		if len(row) != 12 {
			t.Fatal("row width", len(row))
		}
		for i, v := range row {
			if v.Type() != in.Source.Columns[i].Usage.Type || !v.ValidResult(in.Source.Columns[i].Usage) {
				t.Fatal("foreign cell authority", i, v)
			}
		}
		if columnsUnitCell(t, row[2]) == "tables_unit" {
			retained = row
		}
	}
	if retained == nil {
		t.Fatal("missing zero-column relation")
	}
	values := func(row []sqltype.Value) []any {
		out := make([]any, len(row))
		for i, v := range row {
			out[i] = columnsUnitCell(t, v)
		}
		return out
	}
	if got := values(retained); !reflect.DeepEqual(got, want) {
		t.Fatal("full domain row", got, want)
	}
	changed := catalog.CloneState(state)
	renamed := *table
	renamed.ObjectName = "renamed"
	changed.SetDescriptor(renamed.Address(), &renamed)
	fresh, err := produce(tablesUnitInput(t, f, changed, f.owner))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range fresh {
		if columnsUnitCell(t, row[2]) == "renamed" {
			found = true
		}
		if columnsUnitCell(t, row[2]) == "tables_unit" {
			t.Fatal("fresh capture retained old name")
		}
	}
	if !found {
		t.Fatal("fresh capture missed rename")
	}
	if got := values(retained); !reflect.DeepEqual(got, want) {
		t.Fatal("retained cells changed", got)
	}
	hidden := catalog.CloneState(state)
	for _, d := range in.Catalog.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok {
			copy := *relation
			copy.Owner = f.owner
			copy.Security.ACL = authorization.ACLValue{Explicit: true}
			copy.Security.ColumnACL = nil
			hidden.SetDescriptor(copy.Address(), &copy)
		}
	}
	empty, err := produce(tablesUnitInput(t, f, hidden, f.reader))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("successful all-hidden capture", empty, err)
	}
}

func TestInfoTablesCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	old := tablesUnitInput(t, f, f.e.catalog.state, f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTablesOID)
	foreign := New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = foreign.catalog.owner() },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
	} {
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("foreign capture accepted", rows, err)
		}
	}
	// These corruptions protect ownership and layout, which public SQL cannot mint.
	for _, kind := range []string{"width", "domain-generation", "base-generation", "origin"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.e.catalog.state)
			source := *old.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			switch kind {
			case "width":
				source.Columns = source.Columns[:11]
			case "domain-generation":
				source.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(13248)
			case "base-generation":
				typ := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				typ.SQLType, _ = foreign.CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(typ.Address(), &typ)
			case "origin":
				source.Columns[0].OriginAttribute = 2
			}
			state.SetDescriptor(source.Address(), &source)
			if rows, err := produce(tablesUnitInput(t, f, state, f.owner)); rows != nil || err == nil {
				t.Fatal("malformed source accepted", rows, err)
			}
		})
	}
	in := old
	in.Context = &dtpCancelContext{Context: context.Background(), remaining: 3}
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("partial canceled capture", rows, err)
	}
}
