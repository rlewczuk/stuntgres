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
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type cduUnitFixture struct {
	f             defaultUnitFixture
	state         *catalog.State
	parent, child *catalog.Type
	table         *catalog.Relation
}

func newCDUUnitFixture(t *testing.T) cduUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	integer, _ := f.e.Catalog.Read(state).LookupType(sqltype.Int4)
	makeDomain := func(id sqltype.ID, name string, base sqltype.Usage) *catalog.Type {
		typ, err := sqltype.Domain(id, name, "default_schema."+name, base, false, false)
		if err != nil {
			t.Fatal(err)
		}
		d := &catalog.Type{SQLType: typ, ObjectNamespace: f.schema, ObjectRevision: 1, Owner: f.owner}
		state.SetDescriptor(d.Address(), d)
		return d
	}
	parent := makeDomain(24100, "parent", integer.Usage())
	child := makeDomain(24101, "child", parent.SQLType.Usage())
	array, err := sqltype.Array(24102, "_parent", parent.SQLType)
	if err != nil {
		t.Fatal(err)
	}
	arrayType := &catalog.Type{SQLType: array, ObjectNamespace: f.schema, Owner: f.owner}
	state.SetDescriptor(arrayType.Address(), arrayType)
	overArray := makeDomain(24103, "over_array", array.Usage())
	columns := []catalog.Column{{Name: "a", Usage: parent.SQLType.Usage()}, {Name: "b", Usage: child.SQLType.Usage()}, {Name: "plain", Usage: integer.Usage()}, {Name: "array_of_domain", Usage: array.Usage()}, {Name: "domain_over_array", Usage: overArray.SQLType.Usage()}, {Name: "dropped", Usage: parent.SQLType.Usage()}, {Name: "system", Usage: parent.SQLType.Usage()}}
	var table *catalog.Relation
	for i, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation, catalog.MaterializedViewRelation, catalog.CompositeRelation, catalog.SequenceRelation, catalog.IndexRelation, catalog.CatalogRelation} {
		names := []string{"table", "view", "foreign", "partitioned", "materialized", "composite", "sequence", "index", "catalog_view"}
		r := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(24200 + i)}, ObjectNamespace: f.schema, ObjectName: names[i], ObjectRevision: 1, RelationKind: kind, Owner: f.reader, Columns: columns, Attributes: []int32{1, 3, 4, 5, 6, 7, -1}, CatalogView: kind == catalog.CatalogRelation}
		r.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{7: {}}
		if kind == catalog.IndexRelation {
			r.SQLIndex = &catalog.IndexDefinition{Parent: table.Address()}
		}
		state.SetDescriptor(r.Address(), r)
		if i == 0 {
			table = r
		}
	}
	return cduUnitFixture{f: f, state: state, parent: parent, child: child, table: table}
}

func (f cduUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnDomainUsageOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func cduUnitExpected() map[[7]string]int {
	want := map[[7]string]int{}
	for _, table := range []string{"table", "view", "foreign", "partitioned", "catalog_view"} {
		for _, pair := range [][2]string{{"parent", "a"}, {"child", "b"}, {"over_array", "domain_over_array"}} {
			want[[7]string{"captured_database", "default_schema", pair[0], "captured_database", "default_schema", table, pair[1]}]++
		}
	}
	return want
}

func cduUnitBag(t *testing.T, rows [][]sqltype.Value) map[[7]string]int {
	t.Helper()
	bag := map[[7]string]int{}
	for _, row := range rows {
		if len(row) != 7 {
			t.Fatal("projection width", len(row))
		}
		var key [7]string
		for i, value := range row {
			if value.IsNull() {
				t.Fatal("NULL identifier", i)
			}
			base, err := value.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			key[i], err = base.Text()
			if err != nil {
				t.Fatal(err)
			}
		}
		bag[key]++
	}
	return bag
}

func cduUnitCheck(t *testing.T, in pgcatalog.Input, want map[[7]string]int) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoColumnDomainUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	captured, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("capture authority/empty source", captured)
	}
	identifier, _ := in.Catalog.LookupType(13248)
	physical, _ := in.Catalog.LookupType(sqltype.Name)
	for _, row := range captured.Anonymous {
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.Type() != identifier || identifier.BaseType() != physical || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoColumnDomainUsageOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
				t.Fatal("source-owned generation/schema/origin", i, value, column)
			}
		}
	}
	// Scope fixture comparisons independently of unrelated installed providers.
	fixtureRows := make([][]sqltype.Value, 0)
	for _, row := range captured.Anonymous {
		base, _ := row[4].BaseValue()
		schema, _ := base.Text()
		if schema == "default_schema" {
			fixtureRows = append(fixtureRows, row)
		}
	}
	if got := cduUnitBag(t, fixtureRows); !reflect.DeepEqual(got, want) {
		t.Fatal("complete fixture bag", got, want)
	}
	return captured.Anonymous
}

func TestInfoColumnDomainUsageProducer(t *testing.T) {
	f := newCDUUnitFixture(t)
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member, f.f.op.Session.Authorization.State.CurrentUser} {
		cduUnitCheck(t, f.input(t, f.state, actor), cduUnitExpected())
	}
	in := f.input(t, f.state, f.f.op.Session.Authorization.State.CurrentUser)
	rows := cduUnitCheck(t, in, cduUnitExpected())
	self := map[[7]string]int{}
	for _, column := range []string{"domain_catalog", "domain_schema", "domain_name", "table_catalog", "table_schema", "table_name", "column_name"} {
		self[[7]string{"captured_database", "information_schema", "sql_identifier", "captured_database", "information_schema", "column_domain_usage", column}] = 1
	}
	actualSelf := map[[7]string]int{}
	for row, count := range cduUnitBag(t, rows) {
		if row[4] == "information_schema" && row[5] == "column_domain_usage" {
			actualSelf[row] = count
		}
	}
	if !reflect.DeepEqual(actualSelf, self) {
		t.Fatal("actual builtin self population", actualSelf, self)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13304})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13303})
	if in.Source.RowType != 13304 || row.OwningRelation != in.Source.Address() || row.Array != 13303 || array.SQLType.Element() != row.SQLType || array.OwningRelation != in.Source.Address() {
		t.Fatal("canonical row/array identity", row, array)
	}
	// Reader owns every referenced table, and domains have default PUBLIC USAGE.
	// Neither authority enables the domain's owner role.
	if hidden := cduUnitCheck(t, f.input(t, f.state, f.f.reader), map[[7]string]int{}); len(hidden) != 0 {
		t.Fatal("owner-disabled identity saw builtin domains")
	}
	granted := catalog.CloneState(f.state)
	for _, d := range []*catalog.Type{f.parent, f.child} {
		copy := *d
		copy.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Role: f.f.reader}, Privileges: authorization.UsagePrivilege, GrantOptions: authorization.UsagePrivilege}}}
		granted.SetDescriptor(copy.Address(), &copy)
	}
	cduUnitCheck(t, f.input(t, granted, f.f.reader), map[[7]string]int{})
}

func TestInfoColumnDomainUsageCapture(t *testing.T) {
	f := newCDUUnitFixture(t)
	t.Run("paired_statement_source", cduUnitPairedCapture)
	old := f.input(t, f.state, f.f.owner)
	retained := cduUnitCheck(t, old, cduUnitExpected())
	oldBag := cduUnitBag(t, retained)
	changed := catalog.CloneState(f.state)
	integer, _ := old.Catalog.LookupType(sqltype.Int4)
	table := *f.table
	table.Columns = append([]catalog.Column(nil), table.Columns...)
	table.Columns[0].Usage = integer.Usage()
	table.Columns[1].Name = "renamed"
	changed.SetDescriptor(table.Address(), &table)
	want := cduUnitExpected()
	delete(want, [7]string{"captured_database", "default_schema", "parent", "captured_database", "default_schema", "table", "a"})
	delete(want, [7]string{"captured_database", "default_schema", "child", "captured_database", "default_schema", "table", "b"})
	want[[7]string{"captured_database", "default_schema", "child", "captured_database", "default_schema", "table", "renamed"}] = 1
	cduUnitCheck(t, f.input(t, changed, f.f.owner), want)
	changed.RemoveDescriptor(table.Address())
	delete(want, [7]string{"captured_database", "default_schema", "child", "captured_database", "default_schema", "table", "renamed"})
	delete(want, [7]string{"captured_database", "default_schema", "over_array", "captured_database", "default_schema", "table", "domain_over_array"})
	cduUnitCheck(t, f.input(t, changed, f.f.owner), want)
	cduUnitCheck(t, old, cduUnitExpected())
	if !reflect.DeepEqual(cduUnitBag(t, retained), oldBag) {
		t.Fatal("retained backing changed")
	}
	owned := catalog.CloneState(f.state)
	domain := *f.parent
	domain.Owner = f.f.reader
	owned.SetDescriptor(domain.Address(), &domain)
	ownerWant := cduUnitExpected()
	readerWant := map[[7]string]int{}
	for key := range ownerWant {
		if key[2] == "parent" {
			delete(ownerWant, key)
			readerWant[key] = 1
		}
	}
	cduUnitCheck(t, f.input(t, owned, f.f.owner), ownerWant)
	cduUnitCheck(t, f.input(t, owned, f.f.reader), readerWant)
	renamed := catalog.CloneState(f.state)
	child := *f.child
	var renameErr error
	child.SQLType, renameErr = child.SQLType.RebindDomain(child.SQLType.DomainBase(), "renamed_child", "default_schema.renamed_child")
	if renameErr != nil {
		t.Fatal(renameErr)
	}
	renamed.SetDescriptor(child.Address(), &child)
	for _, descriptor := range old.Catalog.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Namespace() == f.f.schema {
			copy := *relation
			copy.Columns = append([]catalog.Column(nil), relation.Columns...)
			for i := range copy.Columns {
				if copy.Columns[i].Usage.Type == f.child.SQLType {
					copy.Columns[i].Usage.Type = child.SQLType
				}
			}
			renamed.SetDescriptor(copy.Address(), &copy)
		}
	}
	renameWant := map[[7]string]int{}
	for key, count := range cduUnitExpected() {
		if key[2] == "child" {
			key[2] = "renamed_child"
		}
		renameWant[key] = count
	}
	cduUnitCheck(t, f.input(t, renamed, f.f.owner), renameWant)
	cduUnitCheck(t, old, cduUnitExpected())
	oldMember := f.input(t, f.state, f.f.member)
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	setFixtureEdges(roles, nil)
	f.f.e.RoleStore.PublishLocked(roles)
	cduUnitCheck(t, f.input(t, f.state, f.f.member), map[[7]string]int{})
	cduUnitCheck(t, oldMember, cduUnitExpected())
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnDomainUsageOID)
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
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, kind := range []string{"width", "domain", "generation", "base-generation", "typmod", "collation", "declared-generation", "namespace", "owner"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			switch kind {
			case "namespace", "owner":
				d := *f.parent
				if kind == "namespace" {
					d.ObjectNamespace.ObjectID = 999999
				} else {
					d.Owner = authorization.PrincipalRef{}
				}
				state.SetDescriptor(d.Address(), &d)
			case "base-generation":
				d := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				d.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(d.Address(), &d)
			case "declared-generation":
				d := *f.parent
				var err error
				d.SQLType, err = d.SQLType.RebindDomain(d.SQLType.DomainBase(), d.Name(), d.SQLType.SQLName())
				if err != nil {
					t.Fatal(err)
				}
				state.SetDescriptor(d.Address(), &d)
			default:
				d := *old.Source
				d.Columns = append([]catalog.Column(nil), d.Columns...)
				switch kind {
				case "width":
					d.Columns = d.Columns[:6]
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
				state.SetDescriptor(d.Address(), &d)
			}
			if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("malformed captured definition accepted", rows, err)
			}
		})
	}
}

func TestInfoColumnDomainUsageCancellation(t *testing.T) {
	f := newCDUUnitFixture(t)
	in := f.input(t, f.state, f.f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnDomainUsageOID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precanceled capture", rows, err)
	}
	// Count descriptor/column checkpoints to interrupt a real admitted relation
	// after its first row has been emitted, regardless of descriptor ordering.
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
			t.Fatal("cancellation exposed prefix", remaining, rows, err)
		}
	}
}

func cduUnitPairedCapture(t *testing.T) {
	t.Helper()
	f := newCDUUnitFixture(t)
	f.f.e.Catalog.PublishLocked(f.state)
	f.f.op.Session.DatabaseName = "captured_database"
	f.f.actor(f.f.owner)
	scope, err := f.f.e.StartStatement(f.f.op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	f.f.e.Mu.Lock()
	err = f.f.e.CaptureCommentSnapshotLocked(scope)
	f.f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := scope.Inquiry.CommentCatalog
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnDomainUsageOID})
	checkSource := func(t *testing.T) {
		t.Helper()
		f.f.e.Mu.Lock()
		captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		if captured.Ref != view.Owner().Domain.Ref(view.Epoch(), source.Address()) || captured.View != nil || captured.Anonymous == nil {
			t.Fatal("paired statement source authority", captured)
		}
		identifier, _ := view.LookupType(13248)
		physical, _ := view.LookupType(sqltype.Name)
		fixtureRows := make([][]sqltype.Value, 0)
		for _, row := range captured.Anonymous {
			for i, value := range row {
				if value.Type() != identifier || identifier.BaseType() != physical || !value.ValidResult(source.Columns[i].Usage) {
					t.Fatal("paired statement domain authority", i, value)
				}
			}
			base, _ := row[4].BaseValue()
			schema, _ := base.Text()
			if schema == "default_schema" {
				fixtureRows = append(fixtureRows, row)
			}
		}
		if !reflect.DeepEqual(cduUnitBag(t, fixtureRows), cduUnitExpected()) {
			t.Fatal("source resampled publication rather than paired capture")
		}
	}
	t.Run("current_publication", checkSource)
	next := catalog.CloneState(f.state)
	for _, descriptor := range view.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Namespace() == f.f.schema {
			next.RemoveDescriptor(relation.Address())
		}
	}
	f.f.e.Catalog.PublishLocked(next)
	t.Run("retained_publication", checkSource)
	cduUnitCheck(t, f.input(t, next, f.f.owner), map[[7]string]int{})
}
