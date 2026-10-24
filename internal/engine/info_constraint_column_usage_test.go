package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ccuUnitFixture struct {
	f             defaultUnitFixture
	state         *catalog.State
	parent, child *catalog.Relation
}

func newCCUUnitFixture(t *testing.T) ccuUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.catalog.State)
	view := f.e.catalog.Read(state)
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	relation := func(oid ids.OID, name string) *catalog.Relation {
		return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: f.schema, ObjectName: name, ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege|authorization.ReferencesPrivilege, 0)}}}, Attributes: []int32{1, 3}, Columns: []catalog.Column{{Name: "a", Usage: integer.Usage()}, {Name: "b", Usage: integer.Usage()}}}
	}
	parent := relation(20500, "ccu_unit_parent")
	parent.Attributes = append(parent.Attributes, 4)
	parent.Columns = append(parent.Columns, catalog.Column{Name: "payload", Usage: integer.Usage()})
	index := relation(20501, "ccu_unit_index")
	index.RelationKind = catalog.IndexRelation
	index.SQLIndex = &catalog.IndexDefinition{Parent: parent.Address(), Unique: true, Keys: []catalog.IndexKey{{Attribute: 3}, {Attribute: 1}}, Include: []int32{4}}
	parent.Constraints.Unique = []catalog.UniqueConstraint{{Name: "p", Attributes: []int32{3, 1, 3}, Index: index.Address(), Primary: true}, {Name: "u", Attributes: []int32{1, 3}, Index: index.Address()}}
	parent.Constraints.NotNull = []catalog.NotNullConstraint{{Name: "nn", Attribute: 1}, {Name: "nn", Attribute: 1}}
	parent.Constraints.PhysicalNotNull = map[int32]catalog.PhysicalNotNullProof{4: {Usage: integer.Usage()}}
	column := func(attribute int32) expr.Expr {
		address := parent.Address()
		address.SubID = attribute
		return plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Origin: view.Owner().Domain.Ref(view.Epoch(), address)}
	}
	isNotNull := func(attribute int32) expr.Expr {
		return plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: column(attribute), Not: true}
	}
	and := func(left, right expr.Expr) expr.Expr {
		return plan.LogicalExpr{Info: expr.Info{Usage: boolean.Usage()}, Op: parser.LogicalAnd, Left: left, Right: right}
	}
	check := and(isNotNull(1), and(isNotNull(3), isNotNull(1)))
	parent.Constraints.Check = []catalog.CheckConstraint{{Name: "ck", Expression: check}, {Name: "ck", Expression: check}, {Name: "constant", Expression: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}}}
	child := relation(20502, "ccu_unit_child")
	child.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{{Name: "same", Attributes: []int32{3, 1}, Target: parent.Address(), Referenced: []int32{1, 3}, Index: index.Address()}}
	second := relation(20503, "ccu_unit_second")
	second.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{{Name: "same", Attributes: []int32{1, 3, 1}, Target: parent.Address(), Referenced: []int32{3, 1, 3}, Index: index.Address()}}
	for _, table := range []*catalog.Relation{parent, index, child, second} {
		state.SetDescriptor(table.Address(), table)
	}
	return ccuUnitFixture{f, state, parent, child}
}

func (f ccuUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ConstraintColumnUsageOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engineCatalogRuntime{auth: auth, database: "unit_database"}}
}

func ccuUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.ConstraintColumnUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	source, err := captureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Anonymous == nil || source.View != nil || source.Ref != in.Ref {
		t.Fatal("captured source identity", source)
	}
	return source.Anonymous
}

func ccuUnitBag(t *testing.T, rows [][]sqltype.Value) map[string]int {
	t.Helper()
	bag := make(map[string]int)
	for _, row := range rows {
		if len(row) != 7 {
			t.Fatal("projection width", row)
		}
		if infoUserMappingsUnitText(t, row[0]) != "unit_database" || infoUserMappingsUnitText(t, row[4]) != "unit_database" {
			t.Fatal("catalog positions", row)
		}
		key := ""
		for _, index := range []int{1, 2, 3, 5, 6} {
			key += "/" + infoUserMappingsUnitText(t, row[index])
		}
		bag[key]++
	}
	return bag
}

func ccuUnitExpected() map[string]int {
	return map[string]int{
		"/default_schema/ccu_unit_parent/a/default_schema/p": 1, "/default_schema/ccu_unit_parent/b/default_schema/p": 1,
		"/default_schema/ccu_unit_parent/a/default_schema/u": 1, "/default_schema/ccu_unit_parent/b/default_schema/u": 1,
		"/default_schema/ccu_unit_parent/a/default_schema/same": 2, "/default_schema/ccu_unit_parent/b/default_schema/same": 2,
		"/default_schema/ccu_unit_parent/a/default_schema/ck": 1, "/default_schema/ccu_unit_parent/b/default_schema/ck": 1,
		"/default_schema/ccu_unit_parent/a/default_schema/nn": 1,
	}
}

func TestInfoConstraintColumnUsageProducer(t *testing.T) {
	f := newCCUUnitFixture(t)
	in := f.input(t, f.state, f.f.owner)
	rows := ccuUnitRows(t, in)
	if got := ccuUnitBag(t, rows); !reflect.DeepEqual(got, ccuUnitExpected()) {
		t.Fatal("constraint bag", got, ccuUnitExpected())
	}
	for _, row := range rows {
		for i, value := range row {
			column := in.Source.Columns[i]
			payload, err := value.BaseValue()
			if err != nil || value.IsNull() || value.Type() != column.Usage.Type || value.Type().ID() != 13248 || payload.Type().ID() != sqltype.Name || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.ConstraintColumnUsageOID) || column.OriginAttribute != int16(i+1) || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || column.Usage.Collation.ID != 950 {
				t.Fatal("carrier/origin", i, value, column, err)
			}
		}
	}
	if in.Source.RowType != 13324 {
		t.Fatal("row identity", in.Source.RowType)
	}
	rowType, ok := in.Catalog.LookupType(13324)
	if !ok {
		t.Fatal("missing row type")
	}
	array, ok := in.Catalog.LookupType(13323)
	if !ok || array.Element() != rowType {
		t.Fatal("canonical array identity")
	}
	descriptor, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13324})
	if !ok || descriptor.OwningRelation != in.Source.Address() {
		t.Fatal("row relation backlink")
	}
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.PartitionedRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.IndexRelation} {
		state := catalog.CloneState(f.state)
		parent := *f.parent
		parent.RelationKind = kind
		if kind == catalog.IndexRelation {
			parent.SQLIndex = &catalog.IndexDefinition{Parent: f.child.Address()}
		}
		state.SetDescriptor(parent.Address(), &parent)
		got := ccuUnitBag(t, ccuUnitRows(t, f.input(t, state, f.f.owner)))
		want := map[string]int{}
		if kind == catalog.TableRelation || kind == catalog.PartitionedRelation {
			want = ccuUnitExpected()
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("projected relation kind", kind, got, want)
		}
	}
	// Retired attributes in actual key vectors do not become NULL placeholders.
	state := catalog.CloneState(f.state)
	parent := *f.parent
	parent.Attributes = []int32{1, 4}
	parent.Columns = []catalog.Column{parent.Columns[0], parent.Columns[2]}
	parent.Constraints.Check = nil
	state.SetDescriptor(parent.Address(), &parent)
	want := ccuUnitExpected()
	for key := range want {
		if key == "/default_schema/ccu_unit_parent/a/default_schema/ck" || key == "/default_schema/ccu_unit_parent/b/default_schema/ck" || key == "/default_schema/ccu_unit_parent/b/default_schema/p" || key == "/default_schema/ccu_unit_parent/b/default_schema/u" || key == "/default_schema/ccu_unit_parent/b/default_schema/same" {
			delete(want, key)
		}
	}
	if got := ccuUnitBag(t, ccuUnitRows(t, f.input(t, state, f.f.owner))); !reflect.DeepEqual(got, want) {
		t.Fatal("dropped attribute bag", got, want)
	}
	empty := f.input(t, catalog.CloneState(f.f.e.catalog.State), f.f.owner)
	if rows := ccuUnitRows(t, empty); rows == nil || len(rows) != 0 {
		t.Fatal("successful emptiness", rows)
	}
}

func TestInfoConstraintColumnUsageCapture(t *testing.T) {
	f := newCCUUnitFixture(t)
	in := f.input(t, f.state, f.f.owner)
	held := ccuUnitRows(t, in)
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member} {
		if got := ccuUnitBag(t, ccuUnitRows(t, f.input(t, f.state, actor))); !reflect.DeepEqual(got, ccuUnitExpected()) {
			t.Fatal("owner inheritance", got)
		}
	}
	if rows := ccuUnitRows(t, f.input(t, f.state, f.f.reader)); len(rows) != 0 {
		t.Fatal("ACL-only metadata", rows)
	}
	changed := catalog.CloneState(f.state)
	parent := *f.parent
	parent.ObjectName = "ccu_unit_renamed"
	parent.Owner = f.f.reader
	parent.Constraints.Unique = nil
	changed.SetDescriptor(parent.Address(), &parent)
	if rows := ccuUnitRows(t, f.input(t, changed, f.f.owner)); len(rows) != 0 {
		t.Fatal("child ownership substituted for target owner", rows)
	}
	fresh := ccuUnitRows(t, f.input(t, changed, f.f.reader))
	want := map[string]int{"/default_schema/ccu_unit_renamed/a/default_schema/same": 2, "/default_schema/ccu_unit_renamed/b/default_schema/same": 2, "/default_schema/ccu_unit_renamed/a/default_schema/ck": 1, "/default_schema/ccu_unit_renamed/b/default_schema/ck": 1, "/default_schema/ccu_unit_renamed/a/default_schema/nn": 1}
	if got := ccuUnitBag(t, fresh); !reflect.DeepEqual(got, want) {
		t.Fatal("fresh generation", got, want)
	}
	if got := ccuUnitBag(t, held); !reflect.DeepEqual(got, ccuUnitExpected()) {
		t.Fatal("retained generation changed", got)
	}
	// Change only child ownership; FK rows still follow the original target owner.
	childState := catalog.CloneState(f.state)
	child := *f.child
	child.Owner = f.f.reader
	childState.SetDescriptor(child.Address(), &child)
	if got := ccuUnitBag(t, ccuUnitRows(t, f.input(t, childState, f.f.owner))); !reflect.DeepEqual(got, ccuUnitExpected()) {
		t.Fatal("child owner hid target rows", got)
	}
	if rows := ccuUnitRows(t, f.input(t, childState, f.f.reader)); len(rows) != 0 {
		t.Fatal("child owner exposed target rows", rows)
	}
	// Peer TEMP is deliberately included for an enabled target owner.
	temporary := catalog.CloneState(f.state)
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20510}, ObjectName: "pg_temp_ccu_unit", ObjectRevision: 1, Owner: f.f.owner, Temporary: in.Owner.Domain.Session(100)}
	temporary.SetDescriptor(ns.Address(), ns)
	tempParent := *f.parent
	tempParent.ObjectNamespace = ns.Address()
	temporary.SetDescriptor(tempParent.Address(), &tempParent)
	tempRows := ccuUnitRows(t, f.input(t, temporary, f.f.owner))
	if len(tempRows) != 11 {
		t.Fatal("peer TEMP omitted", tempRows)
	}
	for _, row := range tempRows {
		if infoUserMappingsUnitText(t, row[1]) != ns.Name() {
			t.Fatal("TEMP target namespace", row)
		}
	}
	// Replacing the published role root cannot mutate an already captured read.
	memberInput := f.input(t, f.state, f.f.member)
	f.f.e.mu.Lock()
	nextRoles := cloneRoleFixture(f.f.e.roles.Published)
	setFixtureEdges(nextRoles, nil)
	f.f.e.roles.PublishLocked(nextRoles)
	f.f.e.mu.Unlock()
	if rows := ccuUnitRows(t, f.input(t, f.state, f.f.member)); len(rows) != 0 {
		t.Fatal("fresh revoked inheritance", rows)
	}
	if got := ccuUnitBag(t, ccuUnitRows(t, memberInput)); !reflect.DeepEqual(got, ccuUnitExpected()) {
		t.Fatal("captured role root changed", got)
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.ConstraintColumnUsageOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &New().catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(engineCatalogRuntime)
			runtime.auth.RoleRecord = nil
			in.Runtime = runtime
		},
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid authority accepted", rows, err)
		}
	}
	for _, kind := range []string{"width", "domain", "target", "dependency"} {
		state := catalog.CloneState(f.state)
		switch kind {
		case "width", "domain":
			source := *in.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			if kind == "width" {
				source.Columns = source.Columns[:6]
			} else {
				source.Columns[0].Usage.Type, _ = in.Catalog.LookupType(sqltype.Name)
			}
			state.SetDescriptor(source.Address(), &source)
		case "target":
			child := *f.child
			child.Constraints.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), child.Constraints.ForeignKeys...)
			child.Constraints.ForeignKeys[0].Target.ObjectID = 29999
			state.SetDescriptor(child.Address(), &child)
		case "dependency":
			parent := *f.parent
			address := parent.Address()
			address.SubID = 2
			parent.Constraints.Check = []catalog.CheckConstraint{{Name: "broken", Expression: plan.ColumnExpr{Info: expr.Info{Usage: parent.Columns[0].Usage}, Origin: in.Owner.Domain.Ref(in.Ref.Epoch, address)}}}
			state.SetDescriptor(parent.Address(), &parent)
		}
		if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
			t.Fatal("invalid metadata accepted", kind, rows, err)
		}
	}
}

// Every dependency answer is produced by the real runtime; this wrapper controls
// only when the context becomes canceled relative to that dependency return.
type ccuCancelContext struct {
	context.Context
	armed            bool
	remaining, calls int
}

func (c *ccuCancelContext) Err() error {
	if !c.armed {
		return c.Context.Err()
	}
	c.calls++
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return c.Context.Err()
}

type ccuCancelRuntime struct {
	engineCatalogRuntime
	ctx *ccuCancelContext
}

func (r ccuCancelRuntime) ExpressionReferences(ctx context.Context, view catalog.Read, value expr.Expr) ([]ids.ObjectAddress, error) {
	refs, err := r.engineCatalogRuntime.ExpressionReferences(ctx, view, value)
	if err == nil && len(refs) == 2 {
		r.ctx.armed = true
	}
	return refs, err
}

func TestInfoConstraintColumnUsageCancellation(t *testing.T) {
	f := newCCUUnitFixture(t)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.ConstraintColumnUsageOID)
	base := f.input(t, f.state, f.f.owner)
	for _, remaining := range []int{1, 4} {
		in := base
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("entry/descriptor cancellation exposed rows", rows, err)
		}
	}
	for _, remaining := range []int{1, 4} {
		in := base
		ctx := &ccuCancelContext{Context: t.Context(), remaining: remaining}
		in.Context = ctx
		in.Runtime = ccuCancelRuntime{engineCatalogRuntime: in.Runtime.(engineCatalogRuntime), ctx: ctx}
		// The fourth post-dependency check starts the second reference, after
		// the first member has been fully wrapped and appended. No traversal
		// order, sleep, fake dependency answers or checkpoint sweep is needed.
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) || ctx.calls != remaining {
			t.Fatal("dependency/member cancellation exposed prefix", remaining, ctx.calls, rows, err)
		}
	}
	if rows, err := produce(base); err != nil || len(rows) != 11 {
		t.Fatal("successful populated capture", rows, err)
	}
	empty := f.input(t, catalog.CloneState(f.f.e.catalog.State), f.f.owner)
	if rows, err := produce(empty); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("successful empty capture", rows, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	empty.Context = ctx
	if rows, err := produce(empty); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precanceled empty capture", rows, err)
	}
}
