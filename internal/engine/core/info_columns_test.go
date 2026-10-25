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
func (f columnsUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.dc.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.dc.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
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
func TestInfoColumnsProducer(t *testing.T) {
	f := newColumnsUnitFixture(t)
	in := f.input(t, f.state, f.dc.f.owner)
	rows := columnsUnitRows(t, in)
	if got := columnsUnitTable(t, rows); !reflect.DeepEqual(got, columnsUnitExpected()) {
		t.Fatal("full44 rows", got, columnsUnitExpected())
	}
	self := 0
	for _, row := range rows {
		if columnsUnitCell(t, row[1]) == "information_schema" && columnsUnitCell(t, row[2]) == "columns" {
			self++
		}
	}
	if self != 44 {
		t.Fatal("genuine self description", self)
	}
	if in.Source.Address().ObjectID != 16214 || in.Source.RowType != 13319 {
		t.Fatal("source/row identity", in.Source.Address(), in.Source.RowType)
	}
	rowType := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13319})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13318})
	if rowType.OwningRelation != in.Source.Address() || array.OwningRelation != in.Source.Address() || rowType.Array != 13318 || array.SQLType.Element() != rowType.SQLType {
		t.Fatal("row/array backlinks")
	}
	for i, column := range in.Source.Columns {
		if !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoColumnsOID) || column.OriginAttribute != int16(i+1) || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 {
			t.Fatal("source metadata", i, column)
		}
	}
	hidden := catalog.CloneState(f.state)
	for _, d := range in.Catalog.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok {
			copy := *relation
			copy.Owner = f.dc.f.owner
			copy.Security.ACL = authorization.ACLValue{Explicit: true}
			copy.Security.ColumnACL = nil
			hidden.SetDescriptor(copy.Address(), &copy)
		}
	}
	if rows := columnsUnitRows(t, f.input(t, hidden, f.dc.f.reader)); rows == nil || len(rows) != 0 {
		t.Fatal("typed empty", rows)
	}
}

func TestInfoColumnsCapture(t *testing.T) {
	f := newColumnsUnitFixture(t)
	old := f.input(t, f.state, f.dc.f.owner)
	retained := columnsUnitRows(t, old)
	changed := catalog.CloneState(f.state)
	table := *f.table
	table.Columns = append([]catalog.Column(nil), table.Columns...)
	table.Columns[0].Name = "renamed"
	table.ColumnMeta.Defaults = nil
	changed.SetDescriptor(table.Address(), &table)
	fresh := columnsUnitTable(t, columnsUnitRows(t, f.input(t, changed, f.dc.f.owner)))
	if fresh["renamed"][5] != nil || len(fresh) != 3 {
		t.Fatal("fresh capture", fresh)
	}
	if got := columnsUnitTable(t, columnsUnitRows(t, old)); !reflect.DeepEqual(got, columnsUnitExpected()) || !reflect.DeepEqual(columnsUnitTable(t, retained), got) {
		t.Fatal("retained capture changed", got)
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnsOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = engine.New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.State.CurrentUser = authorization.PrincipalRef{}
			in.Runtime = r
		},
	} {
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("foreign capture accepted", rows, err)
		}
	}
	for _, kind := range []string{"width", "name", "domain", "generation", "base-generation", "typmod", "collation", "nullable", "dimensions", "origin", "virtual", "attributes"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			source := *old.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Attributes = append([]int32(nil), source.Attributes...)
			switch kind {
			case "width":
				source.Columns = source.Columns[:43]
			case "name":
				source.Columns[0].Name = "foreign"
			case "domain":
				source.Columns[0].Usage = source.Columns[4].Usage
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
			if rows, err := produce(f.input(t, state, f.dc.f.owner)); rows != nil || err == nil {
				t.Fatal("bad source accepted", rows, err)
			}
		})
	}
	columnsUnitPairedCapture(t)
}

type columnsCancelRuntime struct {
	core.EngineCatalogRuntime
	cancel context.CancelFunc
	calls  *int
}

func (r columnsCancelRuntime) ColumnExpression(ctx context.Context, view catalog.Read, relation *catalog.Relation, expression expr.Expr) (string, error) {
	*r.calls++
	if *r.calls == 2 {
		r.cancel()
	}
	return r.EngineCatalogRuntime.ColumnExpression(ctx, view, relation, expression)
}
func TestInfoColumnsCancellation(t *testing.T) {
	f := newColumnsUnitFixture(t)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnsOID)
	in := f.input(t, f.state, f.dc.f.owner)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled", rows, err)
	}
	state := catalog.CloneState(f.state)
	table := *f.table
	boolean, _ := in.Catalog.LookupType(sqltype.Bool)
	table.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: f.table.ColumnMeta.Defaults[1], 3: {OID: 22501, Expression: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}}}
	state.SetDescriptor(table.Address(), &table)
	in = f.input(t, state, f.dc.f.owner)
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	in.Context = ctx
	calls := 0
	in.Runtime = columnsCancelRuntime{EngineCatalogRuntime: in.Runtime.(core.EngineCatalogRuntime), cancel: cancel, calls: &calls}
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatal("deparse cancellation published a prefix", rows, err, calls)
	}
	in = f.input(t, f.state, f.dc.f.owner)
	in.Context = &dtpCancelContext{Context: context.Background(), remaining: 2}
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("enumeration cancellation", rows, err)
	}
}

func columnsUnitPairedCapture(t *testing.T) {
	t.Helper()
	f := newColumnsUnitFixture(t)
	e := f.dc.f.e
	e.Catalog.PublishLocked(f.state)
	f.dc.f.op.Session.DatabaseName = "captured_database"
	f.dc.f.actor(f.dc.f.member)
	scope, err := e.StartStatement(f.dc.f.op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	})
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	old := scope.Inquiry.CommentCatalog
	source := old.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnsOID})
	next := catalog.CloneState(f.state)
	table := *f.table
	table.ColumnMeta.Defaults = nil
	table.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.dc.f, authorization.SelectPrivilege, 0)}}
	next.SetDescriptor(table.Address(), &table)
	roles := cloneRoleFixture(e.RoleStore.Published)
	var edges []authorization.Membership
	for _, edge := range fixtureEdges(roles) {
		if edge.Member != f.dc.f.member || edge.Role != f.dc.f.owner {
			edges = append(edges, edge)
		}
	}
	setFixtureEdges(roles, edges)
	e.Catalog.PublishLocked(next)
	e.RoleStore.PublishLocked(roles)
	e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(e, scope, old.Owner().Domain.Ref(old.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil || captured == nil {
		t.Fatal("paired columns source", err)
	}
	defer captured.Close()
	if got := columnsUnitTable(t, captured.Anonymous); !reflect.DeepEqual(got, columnsUnitExpected()) {
		t.Fatal("mixed catalog/roles/default frontier", got)
	}
	if got := columnsUnitTable(t, columnsUnitRows(t, f.input(t, next, f.dc.f.member))); len(got) != 0 {
		t.Fatal("new membership snapshot", got)
	}
	fresh := columnsUnitTable(t, columnsUnitRows(t, f.input(t, next, f.dc.f.reader)))
	if len(fresh) != 3 || fresh["a"][5] != nil {
		t.Fatal("new ACL/default snapshot", fresh)
	}
	if got := columnsUnitTable(t, captured.Anonymous); !reflect.DeepEqual(got, columnsUnitExpected()) {
		t.Fatal("retained rows changed", got)
	}
}
