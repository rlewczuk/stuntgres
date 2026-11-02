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

type cuUnitFixture struct{ cduUnitFixture }

func newCUUnitFixture(t *testing.T) cuUnitFixture {
	t.Helper()
	f := newCDUUnitFixture(t)
	view := f.f.e.Catalog.Read(f.state)
	integer := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	integer.Owner = f.f.owner
	integer.ObjectRevision = integer.Revision() + 1
	f.state.SetDescriptor(integer.Address(), &integer)
	parent := *f.parent
	parent.Owner = f.f.reader
	parent.ObjectRevision = parent.Revision() + 1
	f.state.SetDescriptor(parent.Address(), &parent)
	f.parent = &parent
	return cuUnitFixture{f}
}

func (f cuUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnUdtUsageOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Database: "captured_database"})}
}

func cuUnitExpected(actor string) map[[7]string]int {
	want := map[[7]string]int{}
	for _, table := range []string{"table", "view", "foreign", "partitioned", "catalog_view"} {
		if actor == "owner" || actor == "all" {
			for _, pair := range [][3]string{{"pg_catalog", "int4", "a"}, {"pg_catalog", "int4", "plain"}, {"default_schema", "_parent", "array_of_domain"}, {"default_schema", "_parent", "domain_over_array"}} {
				want[[7]string{"captured_database", pair[0], pair[1], "captured_database", "default_schema", table, pair[2]}] = 1
			}
		}
		if actor == "reader" || actor == "all" {
			want[[7]string{"captured_database", "default_schema", "parent", "captured_database", "default_schema", table, "b"}] = 1
		}
	}
	return want
}

func cuUnitScoped(t *testing.T, rows [][]sqltype.Value) map[[7]string]int {
	t.Helper()
	bag := map[[7]string]int{}
	for key, count := range cduUnitBag(t, rows) {
		if key[4] == "default_schema" {
			bag[key] = count
		}
	}
	return bag
}

func cuUnitCheck(t *testing.T, in pgcatalog.Input, want map[[7]string]int) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoColumnUdtUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	identifier, _ := in.Catalog.LookupType(13248)
	physical, _ := in.Catalog.LookupType(sqltype.Name)
	if len(in.Source.Columns) != 7 || len(in.Source.Attributes) != 7 {
		t.Fatal("source width", in.Source)
	}
	for i, name := range []string{"udt_catalog", "udt_schema", "udt_name", "table_catalog", "table_schema", "table_name", "column_name"} {
		column := in.Source.Columns[i]
		if column.Name != name || column.Usage != identifier.Usage() || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 || column.OriginTable != 16260 || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			t.Fatal("owned nullable identifier schema", i, column)
		}
	}
	captured, err := catalogrt.CaptureRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("capture authority or untyped empty result", captured)
	}
	for _, row := range captured.Anonymous {
		for i, value := range row {
			base, err := value.BaseValue()
			if err != nil || value.IsNull() || value.Type() != identifier || identifier.DomainBase().Type != physical || base.Type() != physical || !value.ValidResult(in.Source.Columns[i].Usage) {
				t.Fatal("source generation/domain/NAME payload", i, value, err)
			}
		}
	}
	if got := cuUnitScoped(t, captured.Anonymous); !reflect.DeepEqual(got, want) {
		t.Fatal("complete declaration bag", got, want)
	}
	return captured.Anonymous
}

func TestInfoColumnUdtUsageProducer(t *testing.T) {
	f := newCUUnitFixture(t)
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member} {
		cuUnitCheck(t, f.input(t, f.state, actor), cuUnitExpected("owner"))
	}
	// Reader owns the represented relations and the parent domain. Only child
	// declarations resolve to that domain; direct parent declarations use int4.
	cuUnitCheck(t, f.input(t, f.state, f.f.reader), cuUnitExpected("reader"))
	in := f.input(t, f.state, f.f.op.Session.Authorization.State.CurrentUser)
	rows := cuUnitCheck(t, in, cuUnitExpected("all"))
	self := map[[7]string]int{}
	for _, column := range in.Source.Columns {
		self[[7]string{"captured_database", "pg_catalog", "name", "captured_database", "information_schema", "column_udt_usage", column.Name}] = 1
	}
	actual := map[[7]string]int{}
	for key, count := range cduUnitBag(t, rows) {
		if key[4] == "information_schema" && key[5] == "column_udt_usage" {
			actual[key] = count
		}
	}
	if !reflect.DeepEqual(actual, self) {
		t.Fatal("builtin self population", actual, self)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13314})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13313})
	if pgcatalog.InfoColumnUdtUsageOID != 16260 || in.Source.RowType != 13314 || !in.Source.VirtualView || in.Source.Name() != "column_udt_usage" || pgcatalog.ClassKind(in.Catalog, in.Source) != "v" || row.OwningRelation != in.Source.Address() || row.Array != 13313 || array.SQLType.Element() != row.SQLType || array.OwningRelation != in.Source.Address() {
		t.Fatal("canonical source/row/array identity", in.Source, row, array)
	}
	if rights := authorization.ACLRights(in.Source.Security.ACL, in.Source.Owner, []authorization.PrincipalRef{f.f.reader}, authorization.TableGrantPrivileges, false); rights != authorization.SelectPrivilege {
		t.Fatal("initial PUBLIC SELECT", rights)
	}
	granted := catalog.CloneState(f.state)
	for _, d := range []*catalog.Type{f.parent, f.child} {
		copy := *d
		copy.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: f.f.reader}, Privileges: authorization.UsagePrivilege, GrantOptions: authorization.UsagePrivilege}}}
		copy.ObjectRevision = copy.Revision() + 1
		granted.SetDescriptor(copy.Address(), &copy)
	}
	cuUnitCheck(t, f.input(t, granted, f.f.reader), cuUnitExpected("reader"))
	oldMember := f.input(t, f.state, f.f.member)
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	edges := fixtureEdges(roles)
	for i := range edges {
		if edges[i].Member == f.f.member {
			edges[i].Inherit, edges[i].Set = false, true
		}
	}
	setFixtureEdges(roles, edges)
	f.f.e.RoleStore.PublishLocked(roles)
	if hidden := cuUnitCheck(t, f.input(t, f.state, f.f.member), map[[7]string]int{}); len(hidden) != 0 {
		t.Fatal("SET-only membership disclosed rows")
	}
	cuUnitCheck(t, oldMember, cuUnitExpected("owner"))
}

func TestInfoColumnUdtUsageCapture(t *testing.T) {
	t.Run("paired_statement_source", cuUnitPairedCapture)
	f := newCUUnitFixture(t)
	old := f.input(t, f.state, f.f.owner)
	retained := cuUnitCheck(t, old, cuUnitExpected("owner"))
	oldBag := cduUnitBag(t, retained)
	backing, cell := &retained[0][0], retained[0][0]
	columns, column := old.Source.Columns, old.Source.Columns[0]
	changed := catalog.CloneState(f.state)
	text, _ := old.Catalog.LookupType(sqltype.Text)
	table := *f.table
	table.Columns = append([]catalog.Column(nil), table.Columns...)
	table.Columns[0].Usage = text.Usage()
	table.Columns[2].Name = "renamed_plain"
	table.ObjectRevision = table.Revision() + 1
	changed.SetDescriptor(table.Address(), &table)
	want := cuUnitExpected("owner")
	delete(want, [7]string{"captured_database", "pg_catalog", "int4", "captured_database", "default_schema", "table", "a"})
	delete(want, [7]string{"captured_database", "pg_catalog", "int4", "captured_database", "default_schema", "table", "plain"})
	want[[7]string{"captured_database", "pg_catalog", "int4", "captured_database", "default_schema", "table", "renamed_plain"}] = 1
	fresh := cuUnitCheck(t, f.input(t, changed, f.f.owner), want)
	if &fresh[0][0] == backing || &old.Source.Columns[0] != &columns[0] || old.Source.Columns[0] != column || &retained[0][0] != backing || !reflect.DeepEqual(retained[0][0], cell) || !reflect.DeepEqual(cduUnitBag(t, retained), oldBag) {
		t.Fatal("returned schema/row backing replaced or shared")
	}
	cuUnitCheck(t, old, cuUnitExpected("owner"))
	owned := catalog.CloneState(f.state)
	parent := *f.parent
	parent.Owner = f.f.owner
	parent.ObjectRevision = parent.Revision() + 1
	owned.SetDescriptor(parent.Address(), &parent)
	cuUnitCheck(t, f.input(t, owned, f.f.owner), cuUnitExpected("all"))
	cuUnitCheck(t, f.input(t, owned, f.f.reader), map[[7]string]int{})
	cuUnitCheck(t, old, cuUnitExpected("owner"))
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnUdtUsageOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) {
			in.Roles = engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Epoch = 0 },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			fields := core.CatalogRuntimeFieldsOf(in.Runtime)
			fields.Auth.RoleRecord = nil
			in.Runtime = core.NewCatalogRuntime(fields)
		},
		func(in *pgcatalog.Input) {
			fields := core.CatalogRuntimeFieldsOf(in.Runtime)
			fields.Auth.State.CurrentUser = authorization.PrincipalRef{}
			in.Runtime = core.NewCatalogRuntime(fields)
		},
	} {
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("foreign capture accepted", rows, err)
		}
	}
	for _, kind := range []string{"width", "attributes", "attribute-number", "domain", "generation", "name-generation", "typmod", "collation", "declared-generation", "base-generation", "namespace", "relation-namespace", "owner", "relation-attributes"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			switch kind {
			case "declared-generation", "base-generation", "namespace", "owner":
				d := *f.parent
				switch kind {
				case "declared-generation", "base-generation":
					var err error
					d.SQLType, err = d.SQLType.RebindDomain(d.SQLType.DomainBase(), d.Name(), d.SQLType.SQLName())
					if err != nil {
						t.Fatal(err)
					}
				case "namespace":
					d.ObjectNamespace.ObjectID = 999999
				case "owner":
					d.Owner = authorization.PrincipalRef{}
				}
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
				if kind == "base-generation" {
					// Keep only the child declaration: a direct parent declaration
					// must not mask the distinguishing immediate-base mismatch.
					for _, descriptor := range old.Catalog.Descriptors() {
						if relation, ok := descriptor.(*catalog.Relation); ok && relation.Namespace() == f.f.schema {
							state.RemoveDescriptor(relation.Address())
						}
					}
					r := *f.table
					r.Columns, r.Attributes = []catalog.Column{f.table.Columns[1]}, []int32{3}
					r.ObjectRevision = r.Revision() + 1
					state.SetDescriptor(r.Address(), &r)
				}
			case "name-generation":
				d := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				d.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Name)
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
			case "relation-namespace", "relation-attributes":
				d := *f.table
				if kind == "relation-namespace" {
					d.ObjectNamespace.ObjectID = 999999
				} else {
					d.Attributes = nil
				}
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
			default:
				d := *old.Source
				d.Columns = append([]catalog.Column(nil), d.Columns...)
				d.Attributes = append([]int32(nil), d.Attributes...)
				switch kind {
				case "width":
					d.Columns = d.Columns[:6]
				case "attributes":
					d.Attributes = nil
				case "attribute-number":
					d.Attributes[1] = 9
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
				t.Fatal("malformed authority published rows", rows, err)
			}
		})
	}
}

func cuUnitPairedCapture(t *testing.T) {
	t.Helper()
	f := newCUUnitFixture(t)
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
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnUdtUsageOID})
	oldColumns := source.Columns
	open := func(scope *core.StatementScope, view catalog.Read, source *catalog.Relation) [][]sqltype.Value {
		f.f.e.Mu.Lock()
		captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		if captured.Anonymous == nil || captured.View != nil {
			t.Fatal("uncaptured source", captured)
		}
		identifier, _ := view.LookupType(13248)
		physical, _ := view.LookupType(sqltype.Name)
		for _, row := range captured.Anonymous {
			for i, value := range row {
				base, err := value.BaseValue()
				if err != nil || value.Type() != identifier || base.Type() != physical || !value.ValidResult(source.Columns[i].Usage) {
					t.Fatal("paired type generation", value, err)
				}
			}
		}
		return captured.Anonymous
	}
	retained := open(scope, view, source)
	if got := cuUnitScoped(t, retained); !reflect.DeepEqual(got, cuUnitExpected("owner")) {
		t.Fatal("initial capture", got)
	}
	next := catalog.CloneState(f.state)
	table := *f.table
	table.Columns = append([]catalog.Column(nil), table.Columns...)
	text, _ := view.LookupType(sqltype.Text)
	table.Columns[0].Usage = text.Usage()
	table.Columns[2].Name = "later_plain"
	table.ObjectRevision = table.Revision() + 1
	next.SetDescriptor(table.Address(), &table)
	liveSource := *source
	liveSource.Security.ACL = authorization.ACLValue{Explicit: true}
	liveSource.ObjectRevision = liveSource.Revision() + 1
	next.SetDescriptor(liveSource.Address(), &liveSource)
	f.f.e.Catalog.PublishLocked(next)
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	setFixtureEdges(roles, nil)
	f.f.e.RoleStore.PublishLocked(roles)
	f.f.actor(f.f.reader)
	f.f.op.Session.DatabaseName = "later_database"
	if got := cuUnitScoped(t, open(scope, view, source)); !reflect.DeepEqual(got, cuUnitExpected("owner")) {
		t.Fatal("resampled live source/catalog/roles/principal/database", got)
	}
	if &source.Columns[0] != &oldColumns[0] || view.Get(source.Address()) != source || next.Get(source.Address()) == source || !reflect.DeepEqual(cuUnitScoped(t, retained), cuUnitExpected("owner")) {
		t.Fatal("captured source/row backing changed")
	}
	cuUnitCheck(t, f.input(t, next, f.f.member), map[[7]string]int{})
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope = nil
	// A new statement captures the later database/identity and the new source,
	// rather than inheriting an old runtime or hiding names behind empty rows.
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
	freshSource := freshView.MustRelation(source.Address())
	if freshSource != &liveSource {
		t.Fatal("fresh source pointer", freshSource)
	}
	want := map[[7]string]int{}
	for key, count := range cuUnitExpected("reader") {
		key[0], key[3] = "later_database", "later_database"
		want[key] = count
	}
	if got := cuUnitScoped(t, open(fresh, freshView, freshSource)); !reflect.DeepEqual(got, want) {
		t.Fatal("fresh capture", got, want)
	}
}

func TestInfoColumnUdtUsageCancellation(t *testing.T) {
	f := newCUUnitFixture(t)
	in := f.input(t, f.state, f.f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnUdtUsageOID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precanceled capture", rows, err)
	}
	checkpoints := 1
	for _, descriptor := range in.Catalog.Descriptors() {
		checkpoints++
		if descriptor.Address() == f.table.Address() {
			break
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			switch pgcatalog.ClassKind(in.Catalog, relation) {
			case "r", "v", "f", "p":
				checkpoints += len(relation.Columns)
			}
		}
	}
	for _, remaining := range []int{2, checkpoints + 2} {
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation published prefix", remaining, rows, err)
		}
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		captured, err := catalogrt.CaptureRows(in, produce)
		if captured != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation published anonymous source", captured, err)
		}
	}
}
