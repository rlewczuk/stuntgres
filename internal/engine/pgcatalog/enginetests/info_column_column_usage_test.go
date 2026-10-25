package enginetests_test

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
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type colcuUnitFixture struct {
	f           defaultUnitFixture
	state       *catalog.State
	table, peer *catalog.Relation
}

func newColcuUnitFixture(t *testing.T) colcuUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	view := f.e.Catalog.Read(state)
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	oid, _ := view.LookupType(sqltype.Oid)
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20550}, ObjectNamespace: f.schema, ObjectName: "colcu_unit", ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Attributes: []int32{1, 3, 4, 5, 6, 7, 8, 9}}
	for _, name := range []string{"a", "b", "s", "v", "z", "o", "c", "n"} {
		table.Columns = append(table.Columns, catalog.Column{Name: name, Usage: integer.Usage()})
	}
	table.Columns[4].Usage = boolean.Usage()
	table.Columns[5].Usage = oid.Usage()
	peer := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20551}, ObjectNamespace: f.schema, ObjectName: "colcu_peer", ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "other", Usage: integer.Usage()}}}
	column := func(relation *catalog.Relation, attribute int32) expr.Expr {
		address := relation.Address()
		address.SubID = attribute
		return plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Origin: view.Owner().Domain.Ref(view.Epoch(), address)}
	}
	literal := func(value bool) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, value)}
	}
	notNull := func(attribute int32) expr.Expr {
		return plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: column(table, attribute), Not: true}
	}
	repeated := plan.LogicalExpr{Info: expr.Info{Usage: boolean.Usage()}, Op: parser.LogicalAnd, Left: notNull(1), Right: plan.LogicalExpr{Info: expr.Info{Usage: boolean.Usage()}, Op: parser.LogicalAnd, Left: notNull(3), Right: notNull(1)}}
	dead := plan.CaseExpr{Info: expr.Info{Usage: boolean.Usage()}, Arms: []plan.CaseBranch{{Condition: literal(false), Result: notNull(1)}}, Otherwise: literal(true)}
	table.Columns[2].Usage = boolean.Usage()
	constant := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: sqltype.Null(integer)}
	seven, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	sevenExpr := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: seven}
	tableoid := plan.TableOIDExpr{Info: expr.Info{Usage: oid.Usage()}, Owner: view.Owner().Domain.Ref(view.Epoch(), table.Address())}
	table.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{
		4: {OID: 20560, Kind: catalog.GenerationStored, Expression: repeated, Planned: repeated},
		5: {OID: 20561, Kind: catalog.GenerationVirtual, Expression: column(table, 3), Planned: column(table, 3)},
		6: {OID: 20562, Kind: catalog.GenerationStored, Expression: dead, Planned: literal(true)},
		7: {OID: 20563, Kind: catalog.GenerationVirtual, Expression: tableoid, Planned: tableoid},
		8: {OID: 20564, Kind: catalog.GenerationStored, Expression: sevenExpr, Planned: sevenExpr},
		9: {OID: 20565, Kind: catalog.GenerationStored, Expression: constant, Planned: constant},
	}
	state.SetDescriptor(table.Address(), table)
	state.SetDescriptor(peer.Address(), peer)
	return colcuUnitFixture{f, state, table, peer}
}
func (f colcuUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnColumnUsageOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "unit_database"}}
}
func colcuUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoColumnColumnUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	source, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Anonymous == nil || source.View != nil || source.Ref != in.Ref {
		t.Fatal("source identity", source)
	}
	return source.Anonymous
}
func colcuUnitBag(t *testing.T, rows [][]sqltype.Value) map[string]int {
	t.Helper()
	bag := make(map[string]int)
	for _, row := range rows {
		if len(row) != 5 {
			t.Fatal("projection width", row)
		}
		if infoUserMappingsUnitText(t, row[0]) != "unit_database" {
			t.Fatal("database", row)
		}
		key := ""
		for _, cell := range row[1:] {
			key += "/" + infoUserMappingsUnitText(t, cell)
		}
		bag[key]++
	}
	return bag
}
func colcuUnitExpected() map[string]int {
	return map[string]int{"/default_schema/colcu_unit/a/s": 1, "/default_schema/colcu_unit/b/s": 1, "/default_schema/colcu_unit/b/v": 1, "/default_schema/colcu_unit/a/z": 1, "/default_schema/colcu_unit/tableoid/o": 1}
}
func TestInfoColumnColumnUsageProducer(t *testing.T) {
	f := newColcuUnitFixture(t)
	in := f.input(t, f.state, f.f.owner)
	names := []string{"table_catalog", "table_schema", "table_name", "column_name", "dependent_column"}
	for i, column := range in.Source.Columns {
		if column.Name != names[i] {
			t.Fatal("column name", i, column.Name)
		}
	}
	rows := colcuUnitRows(t, in)
	if got := colcuUnitBag(t, rows); !reflect.DeepEqual(got, colcuUnitExpected()) {
		t.Fatal("dependency bag", got)
	}
	for _, row := range rows {
		for i, value := range row {
			column := in.Source.Columns[i]
			payload, err := value.BaseValue()
			if err != nil || value.IsNull() || value.Type() != column.Usage.Type || value.Type().ID() != 13248 || payload.Type().ID() != sqltype.Name || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16210 || column.OriginAttribute != int16(i+1) || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || column.Usage.Collation.ID != 950 {
				t.Fatal("domain/origin", i, value, column, err)
			}
		}
	}
	if in.Source.RowType != 13299 {
		t.Fatal("row type", in.Source.RowType)
	}
	row, ok := in.Catalog.LookupType(13299)
	if !ok {
		t.Fatal("missing row type")
	}
	array, ok := in.Catalog.LookupType(13298)
	if !ok || array.Element() != row {
		t.Fatal("array identity")
	}
	descriptor, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13299})
	if !ok || descriptor.OwningRelation != in.Source.Address() {
		t.Fatal("row backlink")
	}
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.PartitionedRelation, catalog.ForeignRelation} {
		state := catalog.CloneState(f.state)
		table := *f.table
		table.RelationKind = kind
		state.SetDescriptor(table.Address(), &table)
		if got := colcuUnitBag(t, colcuUnitRows(t, f.input(t, state, f.f.owner))); !reflect.DeepEqual(got, colcuUnitExpected()) {
			t.Fatal("relation kind", kind, got)
		}
	}
	// The real walker returns these genuine addresses; only same-relation,
	// different-attribute references belong to this view.
	state := catalog.CloneState(f.state)
	table := *f.table
	integer := table.Columns[0].Usage
	own := table.Address()
	own.SubID = 5
	other := f.peer.Address()
	other.SubID = 1
	expression := plan.ConditionalExpr{Info: expr.Info{Usage: integer}, Kind: parser.Coalesce, Args: []expr.Expr{
		plan.ColumnExpr{Info: expr.Info{Usage: integer}, Origin: in.Owner.Domain.Ref(in.Ref.Epoch, own)},
		plan.ColumnExpr{Info: expr.Info{Usage: integer}, Origin: in.Owner.Domain.Ref(in.Ref.Epoch, other)},
	}}
	table.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{5: {OID: 20561, Kind: catalog.GenerationStored, Expression: expression, Planned: expression}}
	domain, err := sqltype.Domain(20570, "colcu_domain", "default_schema.colcu_domain", integer)
	if err != nil {
		t.Fatal(err)
	}
	state.SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 20570}, &catalog.Type{SQLType: domain})
	domainConstant := plan.LiteralExpr{Info: expr.Info{Usage: domain.Usage()}, Value: sqltype.Null(domain)}
	table.ColumnMeta.Generations[8] = catalog.ColumnGeneration{OID: 20564, Kind: catalog.GenerationStored, Expression: domainConstant, Planned: domainConstant}
	state.SetDescriptor(table.Address(), &table)
	if rows := colcuUnitRows(t, f.input(t, state, f.f.owner)); len(rows) != 0 {
		t.Fatal("self/foreign references", rows)
	}
	empty := f.input(t, catalog.CloneState(f.f.e.Catalog.State), f.f.owner)
	if rows := colcuUnitRows(t, empty); rows == nil || len(rows) != 0 {
		t.Fatal("empty capture", rows)
	}
}

func TestInfoColumnColumnUsageCapture(t *testing.T) {
	f := newColcuUnitFixture(t)
	in := f.input(t, f.state, f.f.owner)
	held := colcuUnitRows(t, in)
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member} {
		if got := colcuUnitBag(t, colcuUnitRows(t, f.input(t, f.state, actor))); !reflect.DeepEqual(got, colcuUnitExpected()) {
			t.Fatal("owner inheritance", got)
		}
	}
	if rows := colcuUnitRows(t, f.input(t, f.state, f.f.reader)); len(rows) != 0 {
		t.Fatal("unrelated principal", rows)
	}
	changed := catalog.CloneState(f.state)
	table := *f.table
	table.ColumnMeta.Generations = nil
	changed.SetDescriptor(table.Address(), &table)
	if rows := colcuUnitRows(t, f.input(t, changed, f.f.owner)); len(rows) != 0 {
		t.Fatal("removed definitions", rows)
	}
	if got := colcuUnitBag(t, colcuUnitRows(t, in)); !reflect.DeepEqual(got, colcuUnitExpected()) {
		t.Fatal("retained catalog", got)
	}
	changed = catalog.CloneState(f.state)
	table = *f.table
	table.Owner = f.f.reader
	changed.SetDescriptor(table.Address(), &table)
	if rows := colcuUnitRows(t, f.input(t, changed, f.f.owner)); len(rows) != 0 {
		t.Fatal("changed owner", rows)
	}
	if got := colcuUnitBag(t, colcuUnitRows(t, f.input(t, changed, f.f.reader))); !reflect.DeepEqual(got, colcuUnitExpected()) {
		t.Fatal("new owner", got)
	}
	if got := colcuUnitBag(t, held); !reflect.DeepEqual(got, colcuUnitExpected()) {
		t.Fatal("retained rows", got)
	}
	member := f.input(t, f.state, f.f.member)
	f.f.e.Mu.Lock()
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	setFixtureEdges(roles, nil)
	f.f.e.RoleStore.PublishLocked(roles)
	f.f.e.Mu.Unlock()
	if rows := colcuUnitRows(t, f.input(t, f.state, f.f.member)); len(rows) != 0 {
		t.Fatal("revoked membership", rows)
	}
	if got := colcuUnitBag(t, colcuUnitRows(t, member)); !reflect.DeepEqual(got, colcuUnitExpected()) {
		t.Fatal("retained roles", got)
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnColumnUsageOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = engine.New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.State.CurrentUser = authorization.PrincipalRef{}
			in.Runtime = runtime
		},
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid capture", rows, err)
		}
	}
	for _, kind := range []string{"width", "domain", "attribute", "provider"} {
		state := catalog.CloneState(f.state)
		switch kind {
		case "width", "domain":
			source := *in.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			if kind == "width" {
				source.Columns = source.Columns[:4]
			} else {
				source.Columns[0].Usage.Type, _ = in.Catalog.LookupType(sqltype.Name)
			}
			state.SetDescriptor(source.Address(), &source)
		case "attribute", "provider":
			table := *f.table
			address := table.Address()
			address.SubID = 2
			if kind == "provider" {
				address.ObjectID = 29999
				address.SubID = 1
			}
			tree := plan.ColumnExpr{Info: expr.Info{Usage: table.Columns[0].Usage}, Origin: in.Owner.Domain.Ref(in.Ref.Epoch, address)}
			table.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{4: {OID: 20560, Kind: catalog.GenerationStored, Expression: tree, Planned: tree}}
			state.SetDescriptor(table.Address(), &table)
		}
		if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
			t.Fatal("invalid metadata", kind, rows, err)
		}
	}
}

type colcuCancelRuntime struct {
	core.EngineCatalogRuntime
	ctx *ccuCancelContext
}

func (r colcuCancelRuntime) ExpressionReferences(ctx context.Context, view catalog.Read, value expr.Expr) ([]ids.ObjectAddress, error) {
	refs, err := r.EngineCatalogRuntime.ExpressionReferences(ctx, view, value)
	if err == nil && len(refs) == 2 {
		r.ctx.armed = true
	}
	return refs, err
}

type colcuWalkCancelRuntime struct {
	core.EngineCatalogRuntime
	ctx *ccuCancelContext
}

func (r colcuWalkCancelRuntime) ExpressionReferences(ctx context.Context, view catalog.Read, value expr.Expr) ([]ids.ObjectAddress, error) {
	r.ctx.armed = true
	return r.EngineCatalogRuntime.ExpressionReferences(ctx, view, value)
}
func TestInfoColumnColumnUsageCancellation(t *testing.T) {
	f := newColcuUnitFixture(t)
	base := f.input(t, f.state, f.f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnColumnUsageOID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in := base
	in.Context = ctx
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("entry cancellation", rows, err)
	}
	// Cancel during the real dependency walker (never a substituted answer).
	in = base
	walking := &ccuCancelContext{Context: t.Context(), remaining: 2}
	in.Context = walking
	in.Runtime = colcuWalkCancelRuntime{EngineCatalogRuntime: in.Runtime.(core.EngineCatalogRuntime), ctx: walking}
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("traversal cancellation", rows, err)
	}
	for _, remaining := range []int{1, 2} {
		in = base
		ctx := &ccuCancelContext{Context: t.Context(), remaining: remaining}
		in.Context = ctx
		in.Runtime = colcuCancelRuntime{EngineCatalogRuntime: in.Runtime.(core.EngineCatalogRuntime), ctx: ctx}
		// The second post-walker check follows one fully emitted row, regardless
		// of dependency order; failure must discard that accumulated prefix.
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) || ctx.calls != remaining {
			t.Fatal("partial capture", remaining, ctx.calls, rows, err)
		}
	}
	if rows, err := produce(base); err != nil || len(rows) != 5 {
		t.Fatal("successful capture", rows, err)
	}
}
