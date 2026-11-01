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
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// Share the real constraint/role fixture, not CCU's projection or expectations.
func ctuUnitInput(t *testing.T, f ccuUnitFixture, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	in := f.input(t, state, actor)
	in.Source = in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ConstraintTableUsageOID})
	in.Ref = in.Owner.Domain.Ref(in.Catalog.Epoch(), in.Source.Address())
	return in
}

func ctuUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.ConstraintTableUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	source, err := catalogrt.CaptureRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Anonymous == nil || source.View != nil || source.Ref != in.Ref {
		t.Fatal("captured source identity", source)
	}
	return source.Anonymous
}

func ctuUnitBag(t *testing.T, rows [][]sqltype.Value) map[string]int {
	t.Helper()
	bag := make(map[string]int)
	for _, row := range rows {
		if len(row) != 6 {
			t.Fatal("projection width", row)
		}
		if infoUserMappingsUnitText(t, row[0]) != "unit_database" || infoUserMappingsUnitText(t, row[3]) != "unit_database" {
			t.Fatal("catalog positions", row)
		}
		key := ""
		for _, index := range []int{1, 2, 4, 5} {
			key += "/" + infoUserMappingsUnitText(t, row[index])
		}
		bag[key]++
	}
	return bag
}

func ctuUnitExpected() map[string]int {
	return map[string]int{
		"/default_schema/ccu_unit_parent/default_schema/p":    1,
		"/default_schema/ccu_unit_parent/default_schema/u":    1,
		"/default_schema/ccu_unit_parent/default_schema/same": 2,
	}
}

func TestInfoConstraintTableUsageProducer(t *testing.T) {
	f := newCCUUnitFixture(t)
	in := ctuUnitInput(t, f, f.state, f.f.owner)
	rows := ctuUnitRows(t, in)
	if got := ctuUnitBag(t, rows); !reflect.DeepEqual(got, ctuUnitExpected()) {
		t.Fatal("constraint bag", got, ctuUnitExpected())
	}
	names := []string{"table_catalog", "table_schema", "table_name", "constraint_catalog", "constraint_schema", "constraint_name"}
	domain, _ := in.Catalog.LookupType(13248)
	for _, row := range rows {
		for i, value := range row {
			column := in.Source.Columns[i]
			payload, err := value.BaseValue()
			if err != nil || value.IsNull() || value.Type() != domain || payload.Type() != domain.BaseType() || payload.Type().ID() != sqltype.Name || !value.ValidResult(column.Usage) || column.Usage.Type != domain || column.Name != names[i] || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16252 || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || column.Usage.Collation.ID != 950 {
				t.Fatal("carrier/origin", i, value, column, err)
			}
		}
	}
	if pgcatalog.ConstraintTableUsageOID != 16252 || !in.Source.VirtualView || in.Source.RowType != 13329 {
		t.Fatal("source/row identity", in.Source)
	}
	rowType, ok := in.Catalog.LookupType(13329)
	if !ok {
		t.Fatal("missing row type")
	}
	array, ok := in.Catalog.LookupType(13328)
	if !ok || array.Element() != rowType || rowType.AssociatedArray() != 13328 {
		t.Fatal("canonical array identity")
	}
	descriptor, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13329})
	if !ok || descriptor.OwningRelation != in.Source.Address() {
		t.Fatal("row relation backlink")
	}
	acl := in.Source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.PartitionedRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.IndexRelation} {
		state := catalog.CloneState(f.state)
		parent := *f.parent
		parent.RelationKind = kind
		if kind == catalog.IndexRelation {
			parent.SQLIndex = &catalog.IndexDefinition{Parent: f.child.Address()}
		}
		state.SetDescriptor(parent.Address(), &parent)
		want := map[string]int{}
		if kind == catalog.TableRelation || kind == catalog.PartitionedRelation {
			want = ctuUnitExpected()
		}
		if got := ctuUnitBag(t, ctuUnitRows(t, ctuUnitInput(t, f, state, f.f.owner))); !reflect.DeepEqual(got, want) {
			t.Fatal("projected relation kind", kind, got, want)
		}
	}
	// A table-level relationship does not require any column, key vector or
	// supporting index to be expanded. Ignore CHECK/NOT NULL dependencies too.
	tableOnly := catalog.CloneState(f.state)
	for _, descriptor := range in.Catalog.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.Namespace() != f.f.schema {
			continue
		}
		copy := *table
		copy.Columns, copy.Attributes = nil, nil
		copy.Constraints.Unique = append([]catalog.UniqueConstraint(nil), table.Constraints.Unique...)
		for i := range copy.Constraints.Unique {
			copy.Constraints.Unique[i].Attributes = nil
			copy.Constraints.Unique[i].Index = ids.ObjectAddress{}
		}
		copy.Constraints.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), table.Constraints.ForeignKeys...)
		for i := range copy.Constraints.ForeignKeys {
			copy.Constraints.ForeignKeys[i].Attributes, copy.Constraints.ForeignKeys[i].Referenced = nil, nil
			copy.Constraints.ForeignKeys[i].Index = ids.ObjectAddress{}
		}
		tableOnly.SetDescriptor(copy.Address(), &copy)
	}
	if got := ctuUnitBag(t, ctuUnitRows(t, ctuUnitInput(t, f, tableOnly, f.f.owner))); !reflect.DeepEqual(got, ctuUnitExpected()) {
		t.Fatal("table-only descriptors", got)
	}
	indexOnly := catalog.CloneState(f.state)
	parent := *f.parent
	parent.Constraints.Unique = nil
	indexOnly.SetDescriptor(parent.Address(), &parent)
	want := map[string]int{"/default_schema/ccu_unit_parent/default_schema/same": 2}
	if got := ctuUnitBag(t, ctuUnitRows(t, ctuUnitInput(t, f, indexOnly, f.f.owner))); !reflect.DeepEqual(got, want) {
		t.Fatal("standalone index synthesized a constraint", got)
	}
	// The released predicate restricts the projected table, not the FK child.
	childKind := catalog.CloneState(f.state)
	child := *f.child
	child.RelationKind = catalog.ForeignRelation
	childKind.SetDescriptor(child.Address(), &child)
	if got := ctuUnitBag(t, ctuUnitRows(t, ctuUnitInput(t, f, childKind, f.f.owner))); !reflect.DeepEqual(got, ctuUnitExpected()) {
		t.Fatal("invented child-kind predicate", got)
	}
	empty := ctuUnitInput(t, f, catalog.CloneState(f.f.e.Catalog.State), f.f.owner)
	if rows := ctuUnitRows(t, empty); rows == nil || len(rows) != 0 {
		t.Fatal("successful emptiness", rows)
	}
}

func TestInfoConstraintTableUsageCapture(t *testing.T) {
	f := newCCUUnitFixture(t)
	in := ctuUnitInput(t, f, f.state, f.f.owner)
	held := ctuUnitRows(t, in)
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member} {
		if got := ctuUnitBag(t, ctuUnitRows(t, ctuUnitInput(t, f, f.state, actor))); !reflect.DeepEqual(got, ctuUnitExpected()) {
			t.Fatal("owner inheritance", got)
		}
	}
	if rows := ctuUnitRows(t, ctuUnitInput(t, f, f.state, f.f.reader)); len(rows) != 0 {
		t.Fatal("ACL-only metadata", rows)
	}
	changed := catalog.CloneState(f.state)
	parent := *f.parent
	parent.ObjectName, parent.Owner = "ctu_unit_renamed", f.f.reader
	parent.ObjectRevision++
	changed.SetDescriptor(parent.Address(), &parent)
	if rows := ctuUnitRows(t, ctuUnitInput(t, f, changed, f.f.owner)); len(rows) != 0 {
		t.Fatal("target ownership did not govern all rows", rows)
	}
	want := map[string]int{"/default_schema/ctu_unit_renamed/default_schema/p": 1, "/default_schema/ctu_unit_renamed/default_schema/u": 1, "/default_schema/ctu_unit_renamed/default_schema/same": 2}
	if got := ctuUnitBag(t, ctuUnitRows(t, ctuUnitInput(t, f, changed, f.f.reader))); !reflect.DeepEqual(got, want) {
		t.Fatal("fresh private frontier", got, want)
	}
	f.f.e.Mu.Lock()
	f.f.e.Catalog.PublishLocked(changed)
	f.f.e.Mu.Unlock()
	if got := ctuUnitBag(t, ctuUnitRows(t, ctuUnitInput(t, f, f.f.e.Catalog.State, f.f.reader))); !reflect.DeepEqual(got, want) {
		t.Fatal("fresh published frontier", got, want)
	}
	if got := ctuUnitBag(t, ctuUnitRows(t, in)); !reflect.DeepEqual(got, ctuUnitExpected()) {
		t.Fatal("captured catalog changed", got)
	}
	childState := catalog.CloneState(f.state)
	child := *f.child
	child.Owner = f.f.reader
	childState.SetDescriptor(child.Address(), &child)
	if got := ctuUnitBag(t, ctuUnitRows(t, ctuUnitInput(t, f, childState, f.f.owner))); !reflect.DeepEqual(got, ctuUnitExpected()) {
		t.Fatal("child owner hid target rows", got)
	}
	if rows := ctuUnitRows(t, ctuUnitInput(t, f, childState, f.f.reader)); len(rows) != 0 {
		t.Fatal("child owner exposed target rows", rows)
	}
	memberInput := ctuUnitInput(t, f, f.state, f.f.member)
	f.f.e.Mu.Lock()
	nextRoles := cloneRoleFixture(f.f.e.RoleStore.Published)
	setFixtureEdges(nextRoles, nil)
	f.f.e.RoleStore.PublishLocked(nextRoles)
	f.f.e.Mu.Unlock()
	if rows := ctuUnitRows(t, ctuUnitInput(t, f, f.state, f.f.member)); len(rows) != 0 {
		t.Fatal("fresh revoked inheritance", rows)
	}
	if got := ctuUnitBag(t, ctuUnitRows(t, memberInput)); !reflect.DeepEqual(got, ctuUnitExpected()) {
		t.Fatal("captured role root changed", got)
	}
	if got := ctuUnitBag(t, held); !reflect.DeepEqual(got, ctuUnitExpected()) {
		t.Fatal("retained rows changed", got)
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.ConstraintTableUsageOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = engine.New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) {
			foreign := engine.New().CatalogRead()
			in.Ref = foreign.Owner().Domain.Ref(in.Ref.Epoch, in.Ref.Address)
		},
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = core.NewCatalogRuntime(runtime)
		},
		func(in *pgcatalog.Input) {
			runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
			runtime.Auth.State.CurrentUser = authorization.PrincipalRef{}
			in.Runtime = core.NewCatalogRuntime(runtime)
		},
	} {
		bad := in
		mutate(&bad)
		if source, err := catalogrt.CaptureRows(bad, produce); source != nil || err == nil {
			t.Fatal("invalid authority exposed source", source, err)
		}
	}
	for _, kind := range []string{"width", "domain", "foreign-domain", "missing-domain", "base", "target", "constraint-namespace", "target-namespace"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			switch kind {
			case "width", "domain", "foreign-domain", "base":
				source := *in.Source
				source.Columns = append([]catalog.Column(nil), source.Columns...)
				switch kind {
				case "width":
					source.Columns = source.Columns[:5]
				case "domain":
					source.Columns[0].Usage.Type, _ = in.Catalog.LookupType(sqltype.Name)
				case "foreign-domain":
					source.Columns[0].Usage.Type, _ = engine.New().CatalogRead().LookupType(13248)
				case "base":
					text, _ := in.Catalog.LookupType(sqltype.Text)
					domain, err := sqltype.Domain(13248, "sql_identifier", "information_schema.sql_identifier", text.Usage())
					if err != nil {
						t.Fatal(err)
					}
					descriptor := *in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13248})
					descriptor.SQLType = domain
					state.SetDescriptor(descriptor.Address(), &descriptor)
					for i := range source.Columns {
						source.Columns[i].Usage.Type = domain
					}
				}
				state.SetDescriptor(source.Address(), &source)
			case "missing-domain":
				state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13248})
			case "target":
				child := *f.child
				child.Constraints.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), child.Constraints.ForeignKeys...)
				child.Constraints.ForeignKeys[0].Target.ObjectID = 29999
				state.SetDescriptor(child.Address(), &child)
			case "constraint-namespace":
				child := *f.child
				child.ObjectNamespace.ObjectID = 29999
				state.SetDescriptor(child.Address(), &child)
			case "target-namespace":
				parent := *f.parent
				parent.ObjectNamespace.ObjectID = 29999
				parent.Constraints.Unique = nil
				state.SetDescriptor(parent.Address(), &parent)
			}
			bad := ctuUnitInput(t, f, state, f.f.owner)
			if rows, err := produce(bad); rows != nil || err == nil {
				t.Fatal("invalid metadata exposed rows", rows, err)
			}
			if source, err := catalogrt.CaptureRows(bad, produce); source != nil || err == nil {
				t.Fatal("invalid metadata exposed source", source, err)
			}
		})
	}
}

// CurrentPrincipal is a real projection-admission checkpoint. Delegate the
// authority unchanged, then cancel at the next context check: the first tuple
// has been wrapped/appended by then, without relying on descriptor order.
type ctuCancelRuntime struct {
	pgcatalog.Runtime
	ctx        *ccuCancelContext
	admissions *int
}

func (r ctuCancelRuntime) CurrentPrincipal() authorization.PrincipalRef {
	principal := r.Runtime.CurrentPrincipal()
	(*r.admissions)++
	r.ctx.armed = true
	return principal
}

func TestInfoConstraintTableUsageCancellation(t *testing.T) {
	f := newCCUUnitFixture(t)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.ConstraintTableUsageOID)
	base := ctuUnitInput(t, f, f.state, f.f.owner)
	held := ctuUnitRows(t, base)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, state := range []*catalog.State{f.state, catalog.CloneState(f.f.e.Catalog.State)} {
		in := ctuUnitInput(t, f, state, f.f.owner)
		in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("precanceled capture exposed rows", rows, err)
		}
	}
	in := base
	controlled := &ccuCancelContext{Context: t.Context(), remaining: 1}
	admissions := 0
	in.Context = controlled
	in.Runtime = ctuCancelRuntime{Runtime: in.Runtime, ctx: controlled, admissions: &admissions}
	if source, err := catalogrt.CaptureRows(in, produce); source != nil || !errors.Is(err, context.Canceled) || admissions != 1 || controlled.calls != 1 {
		t.Fatal("mid-capture exposed prefix", source, err, admissions, controlled.calls)
	}
	if got := ctuUnitBag(t, ctuUnitRows(t, base)); !reflect.DeepEqual(got, ctuUnitExpected()) {
		t.Fatal("capture after cancellation", got)
	}
	if got := ctuUnitBag(t, held); !reflect.DeepEqual(got, ctuUnitExpected()) {
		t.Fatal("cancellation changed retained rows", got)
	}
}
