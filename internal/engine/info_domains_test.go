package engine

import (
	"context"
	"errors"
	"reflect"
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

func newDomainsUnitFixture(t *testing.T) dcUnitFixture {
	t.Helper()
	f := newDCUnitFixture(t)
	// The domains producer validates descriptor/name-index coherence. The
	// constraint fixture only needs descriptor addresses, so publish the
	// genuine type name bindings as well for this stronger consumer.
	for _, descriptor := range f.f.e.Catalog.Read(f.state).Descriptors() {
		if d, ok := descriptor.(*catalog.Type); ok && d.Namespace() == f.f.schema {
			f.state.BindName(catalog.DescriptorNameKey(d), d.Address())
		}
	}
	return f
}

func domainsUnitInput(t *testing.T, f dcUnitFixture, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	in := f.input(t, state, actor)
	in.Source = in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoDomainsOID})
	in.Ref = in.Owner.Domain.Ref(in.Catalog.Epoch(), in.Source.Address())
	var err error
	in.TypeNamePath, err = core.ReferencePath(in.Runtime.(core.EngineCatalogRuntime).Auth)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func domainsUnitRow(schema, name, dataType, baseSchema, base string) [27]any {
	return [27]any{0: "captured_database", 1: schema, 2: name, 3: dataType, 19: "captured_database", 20: baseSchema, 21: base, 26: "1"}
}

func domainsUnitInteger(schema, name string) [27]any {
	row := domainsUnitRow(schema, name, "integer", "pg_catalog", "int4")
	row[12], row[13], row[14] = int64(32), int64(2), int64(0)
	return row
}

func domainsUnitExpected() map[[27]any]int {
	rows := map[[27]any]int{}
	rows[domainsUnitInteger("information_schema", "cardinal_number")]++
	for _, name := range []string{"character_data", "yes_or_no"} {
		row := domainsUnitRow("information_schema", name, "character varying", "pg_catalog", "varchar")
		row[5], row[9], row[10], row[11] = int64(1073741824), "captured_database", "pg_catalog", "C"
		if name == "yes_or_no" {
			row[4], row[5] = int64(3), int64(12)
		}
		rows[row]++
	}
	identifier := domainsUnitRow("information_schema", "sql_identifier", "name", "pg_catalog", "name")
	identifier[9], identifier[10], identifier[11] = "captured_database", "pg_catalog", "C"
	rows[identifier]++
	stamp := domainsUnitRow("information_schema", "time_stamp", "timestamp with time zone", "pg_catalog", "timestamptz")
	stamp[15], stamp[18] = int64(2), "CURRENT_TIMESTAMP(2)"
	rows[stamp]++
	for _, name := range []string{"a", "b", "nn", "plain"} {
		rows[domainsUnitInteger("default_schema", name)]++
	}
	rows[domainsUnitRow("default_schema", "child", "USER-DEFINED", "default_schema", "a")]++
	rows[domainsUnitRow("default_schema", "nn_child", "USER-DEFINED", "default_schema", "nn")]++
	return rows
}

func domainsUnitBag(t *testing.T, in pgcatalog.Input, rows [][]sqltype.Value) map[[27]any]int {
	t.Helper()
	bag := map[[27]any]int{}
	ids := [27]sqltype.ID{13248, 13248, 13248, 13246, 13243, 13243, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13243, 13243, 13243, 13246, 13243, 13246, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13248}
	for _, row := range rows {
		if len(row) != 27 {
			t.Fatal("projection width", len(row))
		}
		var key [27]any
		for i, value := range row {
			column := in.Source.Columns[i]
			owned, _ := in.Catalog.LookupType(ids[i])
			if value.Type() != owned || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || column.OriginTable != uint32(pgcatalog.InfoDomainsOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
				t.Fatal("domain generation, description or origin", i, value, column)
			}
			if value.IsNull() {
				continue
			}
			base, err := value.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			if ids[i] == 13243 {
				key[i], err = base.Integer()
			} else {
				key[i], err = base.Text()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		bag[key]++
	}
	return bag
}

func domainsUnitCapture(t *testing.T, in pgcatalog.Input, want map[[27]any]int) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoDomainsOID)
	if !ok {
		t.Fatal("missing domains producer")
	}
	captured, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("capture identity or empty source", captured)
	}
	if got := domainsUnitBag(t, in, captured.Anonymous); !reflect.DeepEqual(got, want) {
		t.Fatal("complete domain bag", got, want)
	}
	return captured.Anonymous
}

func TestInfoDomainsProducer(t *testing.T) {
	f := newDomainsUnitFixture(t)
	want := domainsUnitExpected()
	in := domainsUnitInput(t, f, f.state, f.f.owner)
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member, f.f.op.Session.Authorization.State.CurrentUser} {
		domainsUnitCapture(t, domainsUnitInput(t, f, f.state, actor), want)
	}
	if in.Source.RowType != 13343 {
		t.Fatal("canonical row identity", in.Source.RowType)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13343})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13342})
	if row.OwningRelation != in.Source.Address() || row.Array != 13342 || array.SQLType.Element() != row.SQLType || array.OwningRelation != in.Source.Address() {
		t.Fatal("row/array backlinks", row, array)
	}
	// Even genuine predefined domains use ordinary captured owner/ACL authority.
	hidden := catalog.CloneState(f.state)
	for _, descriptor := range in.Catalog.Descriptors() {
		if d, ok := descriptor.(*catalog.Type); ok && d.SQLType.IsDomain() {
			copy := *d
			copy.Owner, copy.ACL, copy.ACLSyntheticDefault = f.f.owner, authorization.ACLValue{Explicit: true}, false
			hidden.SetDescriptor(copy.Address(), &copy)
		}
	}
	domainsUnitCapture(t, domainsUnitInput(t, f, hidden, f.f.reader), map[[27]any]int{})
}

func TestInfoDomainsCapture(t *testing.T) {
	f := newDomainsUnitFixture(t)
	old := domainsUnitInput(t, f, f.state, f.f.owner)
	want := domainsUnitExpected()
	retained := domainsUnitCapture(t, old, want)
	changed := catalog.CloneState(f.state)
	d := *f.plain
	integer, _ := old.Catalog.LookupType(sqltype.Int4)
	value, err := sqltype.Integer(integer, 11)
	if err != nil {
		t.Fatal(err)
	}
	// Stored literals retain their result-type provider in the authentic
	// dependency manifest, even when the value itself is a plain integer.
	d.DomainDefault = &catalog.ColumnDefault{
		Expression:   plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value},
		Dependencies: []ids.ObjectRef{old.Owner.Domain.Ref(old.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})},
	}
	d.DomainDefaultOwn, d.DomainDefaultText = true, "11"
	d.Owner = f.f.reader
	changed.SetDescriptor(d.Address(), &d)
	changed.UnbindName(catalog.DescriptorNameKey(f.b))
	changed.RemoveDescriptor(f.b.Address())
	nextWant := domainsUnitExpected()
	delete(nextWant, domainsUnitInteger("default_schema", "plain"))
	delete(nextWant, domainsUnitInteger("default_schema", "b"))
	domainsUnitCapture(t, domainsUnitInput(t, f, changed, f.f.owner), nextWant)
	readerWant := domainsUnitExpected()
	for row := range readerWant {
		if row[1] == "default_schema" {
			delete(readerWant, row)
		}
	}
	newRow := domainsUnitInteger("default_schema", "plain")
	newRow[18] = "11"
	readerWant[newRow]++
	domainsUnitCapture(t, domainsUnitInput(t, f, changed, f.f.reader), readerWant)
	d.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.f.reader, Grantee: authorization.ACLGrantee{Role: f.f.owner}, Privileges: authorization.UsagePrivilege}}}
	granted := catalog.CloneState(changed)
	granted.SetDescriptor(d.Address(), &d)
	nextWant[newRow]++
	domainsUnitCapture(t, domainsUnitInput(t, f, granted, f.f.owner), nextWant)
	domainsUnitCapture(t, old, want)
	if !reflect.DeepEqual(domainsUnitBag(t, old, retained), want) {
		t.Fatal("retained rows changed")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoDomainsOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = New().CatalogRead() },
		func(in *pgcatalog.Input) {
			in.Roles = New().RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Epoch = 0 },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { in.TypeNamePath = nil },
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
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	moved := catalog.CloneState(f.state)
	namespace := *old.Catalog.MustNamespace(f.f.schema)
	moved.UnbindName(catalog.DescriptorNameKey(&namespace))
	namespace.ObjectName = "moved_schema"
	moved.SetDescriptor(namespace.Address(), &namespace)
	moved.BindName(catalog.DescriptorNameKey(&namespace), namespace.Address())
	movedWant := map[[27]any]int{}
	for row, count := range want {
		if row[1] == "default_schema" {
			row[1] = "moved_schema"
		}
		if row[20] == "default_schema" {
			row[20] = "moved_schema"
		}
		movedWant[row] = count
	}
	domainsUnitCapture(t, domainsUnitInput(t, f, moved, f.f.owner), movedWant)
	for _, kind := range []string{"width", "domain", "generation", "base-generation", "typmod", "collation", "namespace", "base", "domain-generation", "domain-collation", "owner"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			switch kind {
			case "namespace", "owner", "domain-generation", "domain-collation":
				d := *f.plain
				switch kind {
				case "namespace":
					d.ObjectNamespace.ObjectID = 999999
				case "owner":
					d.Owner = authorization.PrincipalRef{}
				case "domain-generation":
					foreign, _ := New().CatalogRead().LookupType(sqltype.Int4)
					d.SQLType, err = d.SQLType.RebindDomain(foreign.Usage(), d.Name(), d.SQLType.SQLName())
				case "domain-collation":
					state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950})
				}
				state.SetDescriptor(d.Address(), &d)
			case "base":
				state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
			case "base-generation":
				d := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				d.SQLType, _ = New().CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(d.Address(), &d)
			default:
				d := *old.Source
				d.Columns = append([]catalog.Column(nil), d.Columns...)
				switch kind {
				case "width":
					d.Columns = d.Columns[:26]
				case "domain":
					d.Columns[0].Usage.Type, _ = old.Catalog.LookupType(sqltype.Name)
				case "generation":
					typ := d.Columns[0].Usage.Type
					d.Columns[0].Usage.Type, err = typ.RebindDomain(typ.DomainBase(), typ.Name(), typ.SQLName())
				case "typmod":
					d.Columns[0].Usage.Typmod = 7
				case "collation":
					d.Columns[0].Usage.Collation.ID = 0
				}
				state.SetDescriptor(d.Address(), &d)
			}
			if err != nil {
				t.Fatal(err)
			}
			if rows, err := produce(domainsUnitInput(t, f, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("invalid source accepted", rows, err)
			}
		})
	}
	infoDomainsPairedCapture(t)
}

func TestInfoDomainsCancellation(t *testing.T) {
	f := newDomainsUnitFixture(t)
	in := domainsUnitInput(t, f, f.state, f.f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoDomainsOID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precanceled capture exposed rows", rows, err)
	}
	// Cancel after the first domain's row has been emitted, using immutable
	// descriptor order rather than timing or goroutine races.
	position := 0
	for _, descriptor := range in.Catalog.Descriptors() {
		position++
		if d, ok := descriptor.(*catalog.Type); ok && d.SQLType.IsDomain() && d.SQLType.Concrete() {
			break
		}
	}
	for _, remaining := range []int{2, position + 2} {
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled capture exposed prefix", rows, err)
		}
	}
}

func infoDomainsPairedCapture(t *testing.T) {
	t.Helper()
	f := newDomainsUnitFixture(t)
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
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoDomainsOID})
	next := catalog.CloneState(f.state)
	next.UnbindName(catalog.DescriptorNameKey(f.plain))
	next.RemoveDescriptor(f.plain.Address())
	f.f.e.Catalog.PublishLocked(next)
	f.f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	in := domainsUnitInput(t, f, f.state, f.f.owner)
	if !reflect.DeepEqual(domainsUnitBag(t, in, captured.Anonymous), domainsUnitExpected()) {
		t.Fatal("source resampled published state instead of paired capture")
	}
	want := domainsUnitExpected()
	delete(want, domainsUnitInteger("default_schema", "plain"))
	domainsUnitCapture(t, domainsUnitInput(t, f, next, f.f.owner), want)
}

func TestInfoDomainsSearchPathCapture(t *testing.T) {
	f := newDomainsUnitFixture(t)
	publish := func(d *catalog.Type) {
		f.state.SetDescriptor(d.Address(), d)
		f.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	for i, name := range []string{"int4range", "text", "int4", "json"} {
		typ, err := sqltype.Enum(sqltype.ID(23000+i), name, "default_schema."+name, []string{"x"})
		if err != nil {
			t.Fatal(err)
		}
		publish(&catalog.Type{SQLType: typ, ObjectNamespace: f.f.schema, ObjectRevision: 1, Owner: f.f.owner})
	}
	for i, item := range []struct {
		name string
		base sqltype.ID
	}{
		{"range_sample", sqltype.Int4Range},
		{"array_sample", 1009},
		{"integer_array", 1007},
		{"json_sample", sqltype.JSON},
		{"json_array", 199},
	} {
		base, ok := f.f.e.Catalog.Read(f.state).LookupType(item.base)
		if !ok || !base.Concrete() {
			t.Fatal("missing admitted base", item.base)
		}
		typ, err := sqltype.Domain(sqltype.ID(23100+i), item.name, "default_schema."+item.name, base.Usage(), false, false)
		if err != nil {
			t.Fatal(err)
		}
		publish(&catalog.Type{SQLType: typ, ObjectNamespace: f.f.schema, ObjectRevision: 1, Owner: f.f.owner, ACL: authorization.ACLValue{Explicit: true}})
	}
	pg := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	old := domainsUnitInput(t, f, f.state, f.f.owner)
	old.TypeNamePath = []ids.ObjectAddress{f.f.schema, pg}
	want := domainsUnitExpected()
	want[domainsUnitRow("default_schema", "range_sample", "pg_catalog.int4range", "pg_catalog", "int4range")]++
	want[domainsUnitRow("default_schema", "array_sample", "pg_catalog.text[]", "pg_catalog", "_text")]++
	want[domainsUnitRow("default_schema", "integer_array", "integer[]", "pg_catalog", "_int4")]++
	want[domainsUnitRow("default_schema", "json_sample", "json", "pg_catalog", "json")]++
	want[domainsUnitRow("default_schema", "json_array", "json[]", "pg_catalog", "_json")]++
	// The predefined sql_identifier's nonspecial NAME base is not shadowed.
	retained := domainsUnitCapture(t, old, want)
	next := domainsUnitInput(t, f, catalog.CloneState(f.state), f.f.owner)
	next.TypeNamePath = []ids.ObjectAddress{pg, f.f.schema}
	nextWant := map[[27]any]int{}
	for row, count := range want {
		if row[2] == "range_sample" {
			row[3] = "int4range"
		}
		if row[2] == "array_sample" {
			row[3] = "text[]"
		}
		nextWant[row] = count
	}
	domainsUnitCapture(t, next, nextWant)
	// Omitting the domain namespace changes lookup, never row membership.
	next.TypeNamePath = []ids.ObjectAddress{pg}
	domainsUnitCapture(t, next, nextWant)
	domainsUnitCapture(t, old, want)
	if !reflect.DeepEqual(domainsUnitBag(t, old, retained), want) {
		t.Fatal("retained rows changed after type-name path capture")
	}
	invalid := old
	invalid.TypeNamePath = nil
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoDomainsOID)
	if rows, err := produce(invalid); rows != nil || err == nil {
		t.Fatal("missing type-name path authority accepted", rows, err)
	}
}
