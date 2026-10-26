package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type columnOptionsFixture struct {
	f     defaultUnitFixture
	state *catalog.State
	table *catalog.Relation
}

func newColumnOptionsFixture(t *testing.T) columnOptionsFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	integer, _ := f.e.Catalog.Read(state).LookupType(sqltype.Int4)
	table := &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22400},
		ObjectNamespace: f.schema, ObjectName: "options_unit", ObjectRevision: 1,
		Owner: f.owner, RelationKind: catalog.ForeignRelation,
		Columns:    []catalog.Column{{Name: "first", Usage: integer.Usage()}, {Name: "last", Usage: integer.Usage()}},
		Attributes: []int32{1, 3}, Index: map[string]int{"first": 0, "last": 1},
		ColumnMeta: catalog.ColumnMetadata{HighWater: 3, Dropped: map[int32]catalog.AttributeMetadata{2: {}}},
		Security:   catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}},
		Foreign: &catalog.ForeignTableDefinition{Options: []catalog.ForeignOption{{Name: "table", Value: "not a column"}}, Columns: map[int32][]catalog.ForeignOption{
			1: {{Name: "password", Value: "actual=é🙂"}},
			2: {{Name: "dropped", Value: "never"}},
			3: {{Name: "empty", Value: ""}, {Name: "quoted", Value: "a'b\\c  "}},
		}},
	}
	state.SetDescriptor(table.Address(), table)
	return columnOptionsFixture{f: f, state: state, table: table}
}

func (f columnOptionsFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnOptionsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func columnOptionsRows(t *testing.T, in pgcatalog.Input) ([][]sqltype.Value, map[[6]string]int) {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoColumnOptionsOID)
	if !ok {
		t.Fatal("missing producer")
	}
	source, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Ref != in.Ref || source.View != nil || source.Anonymous == nil {
		t.Fatal("anonymous capture authority", source)
	}
	bag := map[[6]string]int{}
	for _, row := range source.Anonymous {
		if len(row) != 6 {
			t.Fatal("width", row)
		}
		var values [6]string
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.IsNull() || value.Type() != column.Usage.Type || !value.ValidResult(column.Usage) {
				t.Fatal("domain authority", i, value)
			}
			base, err := value.BaseValue()
			want := sqltype.Name
			if i == 5 {
				want = sqltype.VarChar
			}
			if err != nil || base.Type().ID() != want {
				t.Fatal("base authority", i, base, err)
			}
			values[i], err = base.Text()
			if err != nil {
				t.Fatal(err)
			}
		}
		bag[values]++
	}
	return source.Anonymous, bag
}

func columnOptionsExpected() map[[6]string]int {
	return map[[6]string]int{
		{"captured_database", "default_schema", "options_unit", "first", "password", "actual=é🙂"}: 1,
		{"captured_database", "default_schema", "options_unit", "last", "empty", ""}:              1,
		{"captured_database", "default_schema", "options_unit", "last", "quoted", "a'b\\c  "}:     1,
	}
}

func TestInfoColumnOptionsProducer(t *testing.T) {
	f := newColumnOptionsFixture(t)
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member} {
		in := f.input(t, f.state, actor)
		_, got := columnOptionsRows(t, in)
		if !reflect.DeepEqual(got, columnOptionsExpected()) {
			t.Fatal("exact stable attributes", got)
		}
	}
	in := f.input(t, f.state, f.f.owner)
	if in.Source.Name() != "column_options" || in.Source.Namespace().ObjectID != 13229 || in.Source.Address().ObjectID != 16231 || !in.Source.VirtualView {
		t.Fatal("source identity", in.Source)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(in.Source.RowType)})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if row.OwningRelation != in.Source.Address() || array.OwningRelation != in.Source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("row/array identity")
	}
	for i, column := range in.Source.Columns {
		if !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 || column.OriginTable != 16231 || column.OriginAttribute != int16(i+1) {
			t.Fatal("source metadata", i, column)
		}
	}
	rows, empty := columnOptionsRows(t, f.input(t, f.f.e.Catalog.State, f.f.owner))
	if rows == nil || len(empty) != 0 {
		t.Fatal("no foreign tables", rows)
	}
	for _, privilege := range []authorization.Privileges{0, authorization.SelectPrivilege, authorization.InsertPrivilege, authorization.UpdatePrivilege, authorization.ReferencesPrivilege, authorization.DeletePrivilege, authorization.TruncatePrivilege, authorization.TriggerPrivilege, authorization.MaintainPrivilege} {
		state := catalog.CloneState(f.state)
		table := *f.table
		table.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, privilege, 0)}}}
		state.SetDescriptor(table.Address(), &table)
		_, got := columnOptionsRows(t, f.input(t, state, f.f.reader))
		want := map[[6]string]int{}
		if privilege&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) != 0 {
			want[[6]string{"captured_database", "default_schema", "options_unit", "first", "password", "actual=é🙂"}] = 1
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("column privilege", privilege, got, want)
		}
	}
	for _, privilege := range []authorization.Privileges{authorization.SelectPrivilege, authorization.InsertPrivilege, authorization.UpdatePrivilege, authorization.ReferencesPrivilege} {
		state := catalog.CloneState(f.state)
		table := *f.table
		table.Security.ACL.Entries = []authorization.ACLEntry{defaultUnitReaderEntry(f.f, privilege, 0)}
		state.SetDescriptor(table.Address(), &table)
		_, got := columnOptionsRows(t, f.input(t, state, f.f.reader))
		if !reflect.DeepEqual(got, columnOptionsExpected()) {
			t.Fatal("table privilege", privilege, got)
		}
	}
	for _, kind := range []string{"ordinary", "unoptioned", "zero-column", "no-foreign-definition", "dropped-live-slot"} {
		state := catalog.CloneState(f.state)
		table := *f.table
		foreign := *table.Foreign
		table.Foreign = &foreign
		switch kind {
		case "ordinary":
			table.RelationKind = catalog.TableRelation
		case "unoptioned":
			foreign.Columns = nil
		case "zero-column":
			table.Columns, table.Attributes = nil, nil
		case "no-foreign-definition":
			table.Foreign = nil
		case "dropped-live-slot":
			table.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{1: {}, 3: {}}
		}
		state.SetDescriptor(table.Address(), &table)
		rows, got := columnOptionsRows(t, f.input(t, state, f.f.owner))
		if rows == nil || len(got) != 0 {
			t.Fatal("fabricated options", kind, got)
		}
	}
}

func TestInfoColumnOptionsCapture(t *testing.T) {
	f := newColumnOptionsFixture(t)
	old := f.input(t, f.state, f.f.owner)
	retained, _ := columnOptionsRows(t, old)
	state := catalog.CloneState(f.state)
	table := *f.table
	table.ObjectName = "renamed"
	table.Columns = append([]catalog.Column(nil), table.Columns...)
	table.Columns[0].Name = "renamed_column"
	foreign := *table.Foreign
	foreign.Columns = map[int32][]catalog.ForeignOption{1: {{Name: "new", Value: "replacement"}}}
	table.Foreign = &foreign
	state.SetDescriptor(table.Address(), &table)
	_, fresh := columnOptionsRows(t, f.input(t, state, f.f.owner))
	want := map[[6]string]int{{"captured_database", "default_schema", "renamed", "renamed_column", "new", "replacement"}: 1}
	if !reflect.DeepEqual(fresh, want) {
		t.Fatal("fresh names/options", fresh)
	}
	oldRows, oldBag := columnOptionsRows(t, old)
	if !reflect.DeepEqual(oldBag, columnOptionsExpected()) || !reflect.DeepEqual(retained, oldRows) || f.table.Foreign.Columns[1][0].Value != "actual=é🙂" {
		t.Fatal("mutated retained authority")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnOptionsOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = engine.New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { in.Runtime = nil },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
	} {
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid capture", rows, err)
		} else {
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("internal capture error has SQLSTATE", err)
			}
		}
	}
	for _, kind := range []string{"width", "name", "domain", "generation", "base-generation", "typmod", "collation", "nullable", "nullability-known", "dimensions", "origin", "virtual", "attributes"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			source := *old.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Attributes = append([]int32(nil), source.Attributes...)
			switch kind {
			case "width":
				source.Columns = source.Columns[:5]
			case "name":
				source.Columns[0].Name = "wrong"
			case "domain":
				source.Columns[0].Usage = source.Columns[5].Usage
			case "generation":
				source.Columns[0].Usage.Type, _ = engine.New().CatalogRead().LookupType(13248)
			case "base-generation":
				typ := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				typ.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(typ.Address(), &typ)
			case "typmod":
				source.Columns[0].Usage.Typmod = 0
			case "collation":
				source.Columns[0].Usage.Collation = sqltype.CollationRef{}
			case "nullable":
				source.Columns[0].Nullable = false
			case "nullability-known":
				source.Columns[0].NullabilityKnown = false
			case "dimensions":
				source.Columns[0].DeclaredDimensions = 1
			case "origin":
				source.Columns[0].OriginAttribute = 2
			case "virtual":
				source.VirtualView = false
			case "attributes":
				source.Attributes[0] = 2
			}
			state.SetDescriptor(source.Address(), &source)
			if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("malformed authority", rows, err)
			}
		})
	}
}

type columnOptionsCancelRuntime struct {
	core.EngineCatalogRuntime
	cancel context.CancelFunc
	calls  *int
}

func (r columnOptionsCancelRuntime) Effective(ref ids.ObjectRef, owner bool) (authorization.Privileges, error) {
	rights, err := r.EngineCatalogRuntime.Effective(ref, owner)
	*r.calls++
	if *r.calls == 2 {
		r.cancel()
	}
	return rights, err
}

func TestInfoColumnOptionsCancellation(t *testing.T) {
	f := newColumnOptionsFixture(t)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnOptionsOID)
	for _, remaining := range []int{1, 4, 12} {
		in := f.input(t, f.state, f.f.owner)
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled prefix", rows, err)
		}
	}
	in := f.input(t, f.state, f.f.owner)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if source, err := core.CaptureCatalogSourceRows(in, produce); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication", source, err)
	}
	state := catalog.CloneState(f.state)
	table := *f.table
	table.Security.ACL.Entries = []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}
	state.SetDescriptor(table.Address(), &table)
	in = f.input(t, state, f.f.reader)
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	in.Context = ctx
	in.Runtime = columnOptionsCancelRuntime{EngineCatalogRuntime: in.Runtime.(core.EngineCatalogRuntime), cancel: cancel, calls: &calls}
	if source, err := core.CaptureCatalogSourceRows(in, produce); source != nil || !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatal("partial capture publication", source, err, calls)
	}
	_, bag := columnOptionsRows(t, f.input(t, f.state, f.f.owner))
	if !reflect.DeepEqual(bag, columnOptionsExpected()) {
		t.Fatal("cancellation mutated catalog", bag)
	}
}
