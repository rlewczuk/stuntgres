package infoschema_test

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
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Literal reserved identity keeps the test-first round independent of new code.
const duUnitOID ids.OID = 16249

type duUnitFixture struct{ dcUnitFixture }

func newDUUnitFixture(t *testing.T) duUnitFixture {
	t.Helper()
	f := newDomainsUnitFixture(t)
	view := f.f.e.Catalog.Read(f.state)
	integer := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	integer.Owner = f.f.owner
	integer.ObjectRevision = integer.Revision() + 1
	f.state.SetDescriptor(integer.Address(), &integer)
	// Parent a is owned by B; its int4 base is owned by A. Its child must
	// therefore be visible to B, not A (no ultimate-base recursion).
	a := *f.a
	a.Owner = f.f.reader
	a.ObjectRevision = a.Revision() + 1
	f.state.SetDescriptor(a.Address(), &a)
	return duUnitFixture{f}
}
func (f duUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: duUnitOID})
	if !ok {
		t.Fatal("missing information_schema.domain_udt_usage source 16249")
	}
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Database: "captured_database"})}
}
func duUnitRow(baseSchema, base, schema, name string) [6]string {
	return [6]string{"captured_database", baseSchema, base, "captured_database", schema, name}
}
func duUnitOwnerRows() map[[6]string]int {
	want := map[[6]string]int{duUnitRow("pg_catalog", "int4", "information_schema", "cardinal_number"): 1, duUnitRow("default_schema", "nn", "default_schema", "nn_child"): 1}
	for _, name := range []string{"a", "b", "nn", "plain"} {
		want[duUnitRow("pg_catalog", "int4", "default_schema", name)] = 1
	}
	return want
}
func duUnitAllRows() map[[6]string]int {
	want := duUnitOwnerRows()
	want[duUnitRow("default_schema", "a", "default_schema", "child")] = 1
	for _, pair := range [][2]string{{"name", "sql_identifier"}, {"varchar", "character_data"}, {"varchar", "yes_or_no"}, {"timestamptz", "time_stamp"}} {
		want[duUnitRow("pg_catalog", pair[0], "information_schema", pair[1])] = 1
	}
	return want
}
func duUnitBag(t *testing.T, in pgcatalog.Input, rows [][]sqltype.Value) map[[6]string]int {
	t.Helper()
	bag := map[[6]string]int{}
	identifier, _ := in.Catalog.LookupType(13248)
	physical, _ := in.Catalog.LookupType(sqltype.Name)
	for _, row := range rows {
		if len(row) != 6 {
			t.Fatal("projection width", len(row))
		}
		var key [6]string
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.IsNull() || value.Type() != identifier || identifier.BaseType() != physical || !value.ValidResult(column.Usage) {
				t.Fatal("foreign generation or invalid domain cell", i, value)
			}
			base, err := value.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			key[i], err = base.Text()
			if err != nil || base.Type() != physical {
				t.Fatal("NAME payload", base, err)
			}
		}
		bag[key]++
	}
	return bag
}
func duUnitCheck(t *testing.T, in pgcatalog.Input, want map[[6]string]int) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(duUnitOID)
	if !ok {
		t.Fatal("missing domain_udt_usage producer")
	}
	captured, err := catalogrt.CaptureRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("capture must own anonymous rows, even when empty", captured)
	}
	if got := duUnitBag(t, in, captured.Anonymous); !reflect.DeepEqual(got, want) {
		t.Fatal("complete domain/base bag", got, want)
	}
	return captured.Anonymous
}

func TestInfoDomainUdtUsageProducer(t *testing.T) {
	f := newDUUnitFixture(t)
	in := f.input(t, f.state, f.f.owner)
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member} {
		duUnitCheck(t, f.input(t, f.state, actor), duUnitOwnerRows())
	}
	duUnitCheck(t, f.input(t, f.state, f.f.reader), map[[6]string]int{duUnitRow("default_schema", "a", "default_schema", "child"): 1})
	duUnitCheck(t, f.input(t, f.state, f.f.op.Session.Authorization.State.CurrentUser), duUnitAllRows())
	if len(in.Source.Columns) != 6 || len(in.Source.Attributes) != 6 || in.Source.RowType != 13339 {
		t.Fatal("source schema or canonical row identity", in.Source)
	}
	for i, name := range []string{"udt_catalog", "udt_schema", "udt_name", "domain_catalog", "domain_schema", "domain_name"} {
		column := in.Source.Columns[i]
		identifier, _ := in.Catalog.LookupType(13248)
		if column.Name != name || column.Usage != identifier.Usage() || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 || column.OriginTable != uint32(duUnitOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			t.Fatal("stable nullable sql_identifier attribute", i, column)
		}
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13339})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13338})
	if row.OwningRelation != in.Source.Address() || row.Array != 13338 || array.SQLType.Element() != row.SQLType || array.OwningRelation != in.Source.Address() {
		t.Fatal("row/array backlinks", row, array)
	}
	if rights := authorization.ACLRights(in.Source.Security.ACL, in.Source.Owner, []authorization.PrincipalRef{f.f.reader}, authorization.TableGrantPrivileges, false); rights != authorization.SelectPrivilege {
		t.Fatal("initial PUBLIC SELECT only", rights, in.Source.Security.ACL)
	}
	// No seeded-name exception: every actual base has the same owner predicate.
	hidden := catalog.CloneState(f.state)
	for _, descriptor := range in.Catalog.Descriptors() {
		if d, ok := descriptor.(*catalog.Type); ok {
			copy := *d
			copy.Owner = f.f.owner
			copy.ObjectRevision = copy.Revision() + 1
			hidden.SetDescriptor(copy.Address(), &copy)
		}
	}
	duUnitCheck(t, f.input(t, hidden, f.f.reader), map[[6]string]int{})
	// Type USAGE (including PUBLIC) and owning the domain do not replace
	// enabled ownership of its immediate base.
	granted := catalog.CloneState(f.state)
	integer := *in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	integer.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Role: f.f.reader}, Privileges: authorization.UsagePrivilege}}}
	integer.ObjectRevision = integer.Revision() + 1
	granted.SetDescriptor(integer.Address(), &integer)
	duUnitCheck(t, f.input(t, granted, f.f.reader), map[[6]string]int{duUnitRow("default_schema", "a", "default_schema", "child"): 1})
	// An explicit SET-only edge is not an enabled-ownership edge.
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	edges := fixtureEdges(roles)
	for i := range edges {
		if edges[i].Member == f.f.member {
			edges[i].Inherit = false
			edges[i].Set = true
		}
	}
	setFixtureEdges(roles, edges)
	f.f.e.RoleStore.PublishLocked(roles)
	duUnitCheck(t, f.input(t, f.state, f.f.member), map[[6]string]int{})
	duUnitCheck(t, f.input(t, f.state, f.f.owner), duUnitOwnerRows())
}

func TestInfoDomainUdtUsageCapture(t *testing.T) {
	f := newDUUnitFixture(t)
	old := f.input(t, f.state, f.f.member)
	want := duUnitOwnerRows()
	retained := duUnitCheck(t, old, want)
	changed := catalog.CloneState(f.state)
	b := *f.b
	var err error
	b.SQLType, err = b.SQLType.RebindDomain(b.SQLType.DomainBase(), "renamed", "default_schema.renamed")
	if err != nil {
		t.Fatal(err)
	}
	b.ObjectRevision = b.Revision() + 1
	changed.UnbindName(catalog.DescriptorNameKey(f.b))
	changed.SetDescriptor(b.Address(), &b)
	changed.BindName(catalog.DescriptorNameKey(&b), b.Address())
	changed.UnbindName(catalog.DescriptorNameKey(f.plain))
	changed.RemoveDescriptor(f.plain.Address())
	integer := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	integer.Owner = f.f.reader
	integer.ObjectRevision = integer.Revision() + 1
	changed.SetDescriptor(integer.Address(), &integer)
	duUnitCheck(t, f.input(t, changed, f.f.owner), map[[6]string]int{duUnitRow("default_schema", "nn", "default_schema", "nn_child"): 1})
	reader := map[[6]string]int{duUnitRow("default_schema", "a", "default_schema", "child"): 1, duUnitRow("pg_catalog", "int4", "information_schema", "cardinal_number"): 1}
	for _, name := range []string{"a", "renamed", "nn"} {
		reader[duUnitRow("pg_catalog", "int4", "default_schema", name)] = 1
	}
	duUnitCheck(t, f.input(t, changed, f.f.reader), reader)
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	setFixtureEdges(roles, nil)
	f.f.e.RoleStore.PublishLocked(roles)
	duUnitCheck(t, f.input(t, changed, f.f.member), map[[6]string]int{})
	duUnitCheck(t, old, want)
	if !reflect.DeepEqual(duUnitBag(t, old, retained), want) {
		t.Fatal("retained rows changed")
	}
	produce, _ := pgcatalog.ProducerFor(duUnitOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) {
			in.Roles = engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
		func(in *pgcatalog.Input) { in.Ref.Epoch++ }, func(in *pgcatalog.Input) { in.Ref.Epoch = 0 },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 }, func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			r := core.CatalogRuntimeFieldsOf(in.Runtime)
			r.Auth.RoleRecord = nil
			in.Runtime = core.NewCatalogRuntime(r)
		},
		func(in *pgcatalog.Input) {
			r := core.CatalogRuntimeFieldsOf(in.Runtime)
			r.Auth.State.CurrentUser = authorization.PrincipalRef{}
			in.Runtime = core.NewCatalogRuntime(r)
		},
	} {
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("forged capture accepted", rows, err)
		}
	}
	for _, kind := range []string{"width", "domain", "generation", "name-generation", "typmod", "collation", "domain-generation", "domain-namespace", "base-namespace", "base-generation", "base-owner"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			switch kind {
			case "domain-namespace":
				d := *f.a
				d.ObjectNamespace.ObjectID = 999999
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
			case "domain-generation":
				d := *f.a
				var err error
				d.SQLType, err = d.SQLType.RebindDomain(d.SQLType.DomainBase(), d.SQLType.Name(), d.SQLType.SQLName())
				if err != nil {
					t.Fatal(err)
				}
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
			case "base-namespace", "base-generation", "base-owner":
				d := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
				if kind == "base-namespace" {
					d.ObjectNamespace.ObjectID = 999999
				}
				if kind == "base-generation" {
					d.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Int4)
				}
				if kind == "base-owner" {
					d.Owner = authorization.PrincipalRef{}
				}
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
			case "name-generation":
				d := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				d.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Name)
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
			default:
				d := *old.Source
				d.Columns = append([]catalog.Column(nil), d.Columns...)
				switch kind {
				case "width":
					d.Columns = d.Columns[:5]
				case "domain":
					d.Columns[0].Usage.Type, _ = old.Catalog.LookupType(sqltype.Name)
				case "generation":
					typ := d.Columns[0].Usage.Type
					var err error
					d.Columns[0].Usage.Type, err = typ.RebindDomain(typ.DomainBase(), typ.Name(), typ.SQLName())
					if err != nil {
						t.Fatal(err)
					}
				case "typmod":
					d.Columns[0].Usage.Typmod = 7
				case "collation":
					d.Columns[0].Usage.Collation.ID = 0
				}
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
			}
			if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("inconsistent capture accepted", rows, err)
			}
		})
	}
	duUnitPairedCapture(t)
}

func duUnitPairedCapture(t *testing.T) {
	t.Helper()
	f := newDUUnitFixture(t)
	f.f.e.Catalog.PublishLocked(f.state)
	f.f.op.Session.DatabaseName = "captured_database"
	f.f.actor(f.f.member)
	scope, err := f.f.e.StartStatement(f.f.op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if scope != nil {
			if err := scope.Finish(nil); err != nil {
				t.Error(err)
			}
		}
	}()
	f.f.e.Mu.Lock()
	err = f.f.e.CaptureCommentSnapshotLocked(scope)
	f.f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := scope.Inquiry.CommentCatalog
	source, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: duUnitOID})
	if !ok {
		t.Fatal("missing source")
	}
	next := catalog.CloneState(f.state)
	next.UnbindName(catalog.DescriptorNameKey(f.plain))
	next.RemoveDescriptor(f.plain.Address())
	f.f.e.Catalog.PublishLocked(next)
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	setFixtureEdges(roles, nil)
	f.f.e.RoleStore.PublishLocked(roles)
	f.f.actor(f.f.reader)
	f.f.op.Session.DatabaseName = "later_database"
	f.f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	in := f.input(t, f.state, f.f.member)
	if !reflect.DeepEqual(duUnitBag(t, in, captured.Anonymous), duUnitOwnerRows()) {
		t.Fatal("source resampled catalog, roles, current principal or database")
	}
	duUnitCheck(t, f.input(t, next, f.f.member), map[[6]string]int{})

	// A later real statement must capture the new database name, not inherit
	// the old inquiry or hide its catalog fields behind an empty result.
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope = nil
	f.f.actor(f.f.owner)
	fresh, err := f.f.e.StartStatement(f.f.op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fresh.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	f.f.e.Mu.Lock()
	err = f.f.e.CaptureCommentSnapshotLocked(fresh)
	f.f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	freshView := fresh.Inquiry.CommentCatalog
	freshSource, ok := freshView.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: duUnitOID})
	if !ok {
		t.Fatal("missing fresh source")
	}
	f.f.e.Mu.Lock()
	freshRows, err := core.EngineOpenRelationSourceLocked(f.f.e, fresh, freshView.Owner().Domain.Ref(freshView.Epoch(), freshSource.Address()), freshSource.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer freshRows.Close()
	wantFresh := map[[6]string]int{
		{"later_database", "pg_catalog", "int4", "later_database", "information_schema", "cardinal_number"}: 1,
		{"later_database", "pg_catalog", "int4", "later_database", "default_schema", "a"}:                   1,
		{"later_database", "pg_catalog", "int4", "later_database", "default_schema", "b"}:                   1,
		{"later_database", "pg_catalog", "int4", "later_database", "default_schema", "nn"}:                  1,
		{"later_database", "default_schema", "nn", "later_database", "default_schema", "nn_child"}:          1,
	}
	freshInput := pgcatalog.Input{Catalog: freshView, Source: freshSource}
	if got := duUnitBag(t, freshInput, freshRows.Anonymous); !reflect.DeepEqual(got, wantFresh) {
		t.Fatal("fresh statement did not capture later database/catalog authority", got, wantFresh)
	}
}

func TestInfoDomainUdtUsageCancellation(t *testing.T) {
	f := newDUUnitFixture(t)
	in := f.input(t, f.state, f.f.op.Session.Authorization.State.CurrentUser)
	produce, ok := pgcatalog.ProducerFor(duUnitOID)
	if !ok {
		t.Fatal("missing producer")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precanceled capture exposed rows", rows, err)
	}
	// Count real descriptor positions, then cancel only after a qualifying
	// domain has been traversed. No sleeps, timing assumptions or large fixture.
	position := 0
	for _, d := range in.Catalog.Descriptors() {
		position++
		if d.Address() == f.a.Address() {
			break
		}
	}
	for _, remaining := range []int{2, position + 2} {
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled iteration exposed prefix", remaining, rows, err)
		}
	}
}
