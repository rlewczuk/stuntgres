package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
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

var udtUnitNames = []string{"user_defined_type_catalog", "user_defined_type_schema", "user_defined_type_name", "user_defined_type_category", "is_instantiable", "is_final", "ordering_form", "ordering_category", "ordering_routine_catalog", "ordering_routine_schema", "ordering_routine_name", "reference_type", "data_type", "character_maximum_length", "character_octet_length", "character_set_catalog", "character_set_schema", "character_set_name", "collation_catalog", "collation_schema", "collation_name", "numeric_precision", "numeric_precision_radix", "numeric_scale", "datetime_precision", "interval_type", "interval_precision", "source_dtd_identifier", "ref_dtd_identifier"}
var udtUnitDomains = []sqltype.ID{13248, 13248, 13248, 13246, 13256, 13256, 13246, 13246, 13248, 13248, 13248, 13246, 13246, 13243, 13243, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13243, 13243, 13243, 13246, 13243, 13248, 13248}

func udtUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoUserDefinedTypesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured db"}}
}
func udtUnitState(t *testing.T, f defaultUnitFixture) *catalog.State {
	t.Helper()
	view := f.e.CatalogRead()
	state := catalog.CloneState(view.State())
	for i, name := range []string{"alpha", "empty", "table_negative"} {
		integer, _ := view.LookupType(sqltype.Int4)
		var members []sqltype.CompositeMember
		if i != 1 {
			members = []sqltype.CompositeMember{{Attribute: 1, Name: "v", Usage: integer.Usage()}}
		}
		typ, err := sqltype.Composite(sqltype.ID(49000+i), name, "public."+name, 0, members)
		if err != nil {
			t.Fatal(err)
		}
		relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(49100 + i)}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, ObjectName: name, ObjectRevision: 1, RelationKind: catalog.CompositeRelation, Owner: f.owner, RowType: typ.ID()}
		if i == 2 {
			relation.RelationKind = catalog.TableRelation
		}
		if len(members) > 0 {
			relation.Columns = []catalog.Column{{Name: "v", Usage: integer.Usage()}}
			relation.Attributes = []int32{1}
		}
		descriptor := &catalog.Type{SQLType: typ, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, Owner: f.owner, OwningRelation: relation.Address(), ACL: authorization.ACLValue{Explicit: true}}
		state.SetDescriptor(relation.Address(), relation)
		state.BindName(catalog.DescriptorNameKey(relation), relation.Address())
		state.SetDescriptor(descriptor.Address(), descriptor)
		state.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
	}
	return state
}
func udtUnitRows(t *testing.T, in pgcatalog.Input, names ...string) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoUserDefinedTypesOID)
	if !ok {
		t.Fatal("missing producer")
	}
	capture, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	if capture.Ref != in.Ref || capture.View != nil || capture.Anonymous == nil {
		t.Fatal("capture ownership", capture)
	}
	var got []string
	for _, row := range capture.Anonymous {
		if len(row) != 29 {
			t.Fatal("width", len(row))
		}
		for i, value := range row {
			if value.Type() != in.Source.Columns[i].Usage.Type || !value.ValidResult(in.Source.Columns[i].Usage) || value.IsNull() != (i >= 5) {
				t.Fatal("domain cell", i, value)
			}
			base, err := value.BaseValue()
			if err != nil || base.Type() != in.Source.Columns[i].Usage.Type.BaseType() || base.IsNull() != (i >= 5) {
				t.Fatal("base carrier", i, base, err)
			}
		}
		name := infoUserMappingsUnitText(t, row[2])
		got = append(got, name)
		want := []string{"captured db", "public", name, "STRUCTURED", "YES"}
		for i, text := range want {
			base, err := row[i].BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			got, err := base.Text()
			if err != nil || got != text {
				t.Fatal("cell", i, got, err)
			}
		}
	}
	sort.Strings(got)
	sort.Strings(names)
	if len(got) != len(names) || len(got) > 0 && !reflect.DeepEqual(got, names) {
		t.Fatalf("names=%v want=%v", got, names)
	}
	return capture.Anonymous
}
func TestInfoUserDefinedTypesShape(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := udtUnitInput(t, f, view, f.owner)
	s := in.Source
	if pgcatalog.InfoUserDefinedTypesOID != 16234 || s.Name() != "user_defined_types" || s.RowType != 13486 || s.Namespace().ObjectID != 13229 || !s.VirtualView || s.RelationKind != catalog.CatalogRelation {
		t.Fatal("source", s)
	}
	if !s.Security.ACL.Explicit || len(s.Security.ACL.Entries) != 1 || !s.Security.ACL.Entries[0].Grantee.Public || s.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", s.Security.ACL)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13486})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13485})
	if row.OwningRelation != s.Address() || row.Array != 13485 || array.OwningRelation != s.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("reciprocal identities")
	}
	if len(s.Columns) != 29 || len(s.ColumnMeta.CatalogAttributes) != 29 {
		t.Fatal("width")
	}
	for i, c := range s.Columns {
		typ, _ := view.LookupType(udtUnitDomains[i])
		collation := uint32(950)
		if udtUnitDomains[i] == 13243 {
			collation = 0
		}
		if c.Name != udtUnitNames[i] || c.Usage != typ.Usage() || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 16234 || c.OriginAttribute != int16(i+1) || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.DeclaredDimensions != 0 {
			t.Fatal("column", i, c)
		}
		a := s.ColumnMeta.CatalogAttributes[i]
		if a.NotNull || a.Collation != collation || a.Dimensions != 0 {
			t.Fatal("attribute", i, a)
		}
	}
	if pgcatalog.BuiltinViewEvents(16234, nil) != 0 {
		t.Fatal("writable builtin")
	}
	udtUnitRows(t, in)
}
func TestInfoUserDefinedTypesCapture(t *testing.T) {
	t.Run("paired_statement_frontier", udtUnitPairedCapture)
	f := newDefaultUnitFixture(t)
	state := udtUnitState(t, f)
	view := f.e.Catalog.Read(state)
	in := udtUnitInput(t, f, view, f.owner)
	first := udtUnitRows(t, in, "alpha", "empty")
	second := udtUnitRows(t, in, "alpha", "empty")
	first[0][0] = sqltype.Null(first[0][0].Type())
	if second[0][0].IsNull() {
		t.Fatal("shared cell backing")
	}
	udtUnitRows(t, udtUnitInput(t, f, view, f.reader))
	udtUnitRows(t, udtUnitInput(t, f, view, f.member), "alpha", "empty")
	changed := catalog.CloneState(state)
	relation := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49100})
	relation.RelationKind = catalog.TableRelation
	changed.SetDescriptor(relation.Address(), &relation)
	udtUnitRows(t, udtUnitInput(t, f, f.e.Catalog.Read(changed), f.owner), "empty")
	roles := cloneRoleFixture(f.e.RoleStore.Published)
	setFixtureEdges(roles, nil)
	f.e.RoleStore.PublishLocked(roles)
	udtUnitRows(t, udtUnitInput(t, f, view, f.member))
	udtUnitRows(t, in, "alpha", "empty")
	for _, row := range second {
		if row[0].Type() != in.Source.Columns[0].Usage.Type || infoUserMappingsUnitText(t, row[0]) != "captured db" {
			t.Fatal("retained capture changed")
		}
	}
}

// Exercise the actual runtime dispatch, not just a manually paired producer Input.
func udtUnitPairedCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := udtUnitState(t, f)
	f.e.Catalog.PublishLocked(state)
	f.op.Session.DatabaseName = "captured db"
	scope, err := f.e.StartStatement(f.op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	f.e.Mu.Lock()
	err = f.e.CaptureCommentSnapshotLocked(scope)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := scope.Inquiry.CommentCatalog
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoUserDefinedTypesOID})
	next := catalog.CloneState(state)
	for _, address := range []ids.ObjectAddress{
		{ClassID: ids.RelationClassID, ObjectID: 49100},
		{ClassID: ids.TypeClassID, ObjectID: 49000},
	} {
		descriptor := view.Get(address)
		next.UnbindName(catalog.DescriptorNameKey(descriptor))
		next.RemoveDescriptor(address)
	}
	f.e.Catalog.PublishLocked(next)
	f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Anonymous == nil || len(captured.Anonymous) != 2 {
		t.Fatal("paired source rows", captured.Anonymous)
	}
	var names []string
	for _, row := range captured.Anonymous {
		if len(row) != 29 {
			t.Fatal("row width", len(row))
		}
		name := infoUserMappingsUnitText(t, row[2])
		names = append(names, name)
		want := []string{"captured db", "public", name, "STRUCTURED", "YES"}
		for i, value := range row {
			if value.Type() != source.Columns[i].Usage.Type || !value.ValidResult(source.Columns[i].Usage) || value.IsNull() != (i >= 5) {
				t.Fatal("paired domain cell", i, value)
			}
			if i < 5 {
				base, err := value.BaseValue()
				if err != nil {
					t.Fatal(err)
				}
				text, err := base.Text()
				if err != nil || text != want[i] {
					t.Fatal("paired value", i, text, err)
				}
			}
		}
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"alpha", "empty"}) {
		t.Fatal("source resampled publication", names)
	}
	udtUnitRows(t, udtUnitInput(t, f, f.e.Catalog.Read(next), f.owner), "empty")
}
func TestInfoUserDefinedTypesAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := udtUnitState(t, f)
	view := f.e.Catalog.Read(state)
	in := udtUnitInput(t, f, view, f.owner)
	produce, _ := pgcatalog.ProducerFor(16234)
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner }, func(in *pgcatalog.Input) { in.Roles = foreign.Roles() }, func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() }, func(in *pgcatalog.Input) { in.Ref.Epoch++ }, func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = r
		},
	} {
		bad := in
		mutate(&bad)
		rows, err := produce(bad)
		schemataUnitFailure(t, rows, err)
	}
	for column := range 29 {
		for _, kind := range []string{"primitive", "foreign_domain", "typmod", "collation"} {
			bad := catalog.CloneState(state)
			source := *in.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			switch kind {
			case "primitive":
				source.Columns[column].Usage.Type = source.Columns[column].Usage.Type.BaseType()
			case "foreign_domain":
				source.Columns[column].Usage.Type, _ = foreign.CatalogRead().LookupType(udtUnitDomains[column])
			case "typmod":
				source.Columns[column].Usage.Typmod = 7
			case "collation":
				source.Columns[column].Usage.Collation.ID++
			}
			bad.SetDescriptor(source.Address(), &source)
			rows, err := produce(udtUnitInput(t, f, f.e.Catalog.Read(bad), f.owner))
			schemataUnitFailure(t, rows, err)
		}
	}
	for _, id := range []sqltype.ID{sqltype.Name, sqltype.VarChar, sqltype.Int4} {
		bad := catalog.CloneState(state)
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}
		typ := *view.MustType(address)
		typ.SQLType, _ = foreign.CatalogRead().LookupType(id)
		bad.SetDescriptor(address, &typ)
		rows, err := produce(udtUnitInput(t, f, f.e.Catalog.Read(bad), f.owner))
		schemataUnitFailure(t, rows, err)
	}
	for _, kind := range []string{"missing_type", "reciprocal", "namespace", "owner", "member", "address"} {
		bad := catalog.CloneState(state)
		rel := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49100})
		typ := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 49000})
		switch kind {
		case "missing_type":
			bad.RemoveDescriptor(typ.Address())
		case "reciprocal":
			typ.OwningRelation = ids.ObjectAddress{}
			bad.SetDescriptor(typ.Address(), &typ)
		case "namespace":
			rel.ObjectNamespace.ObjectID = 99999
		case "owner":
			rel.Owner = f.reader
		case "member":
			rel.Columns = append([]catalog.Column(nil), rel.Columns...)
			rel.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Int4)
		case "address":
			rel.ObjectAddress.ObjectID++
		}
		bad.SetDescriptor(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49100}, &rel)
		rows, err := produce(udtUnitInput(t, f, f.e.Catalog.Read(bad), f.owner))
		schemataUnitFailure(t, rows, err)
	}
	for _, owner := range []authorization.PrincipalRef{{}, foreign.Roles().LookupRef("postgres"), f.e.RoleStore.Domain.Principal(999999)} {
		bad := catalog.CloneState(state)
		rel := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49100})
		typ := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 49000})
		rel.Owner, typ.Owner = owner, owner
		bad.SetDescriptor(rel.Address(), &rel)
		bad.SetDescriptor(typ.Address(), &typ)
		rows, err := produce(udtUnitInput(t, f, f.e.Catalog.Read(bad), f.owner))
		schemataUnitFailure(t, rows, err)
	}
	for _, kind := range []string{"missing_namespace", "namespace_address", "namespace_binding"} {
		bad := catalog.CloneState(state)
		ns := *view.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200})
		switch kind {
		case "missing_namespace":
			bad.RemoveDescriptor(ns.Address())
		case "namespace_address":
			address := ns.Address()
			ns.ObjectAddress.ObjectID++
			bad.SetDescriptor(address, &ns)
		case "namespace_binding":
			ns.ObjectName = "unbound"
			bad.SetDescriptor(ns.Address(), &ns)
		}
		rows, err := produce(udtUnitInput(t, f, f.e.Catalog.Read(bad), f.owner))
		schemataUnitFailure(t, rows, err)
	}
	// A coherent new domain/base generation is accepted without retagging old rows.
	retained := udtUnitRows(t, in, "alpha", "empty")
	replacement := catalog.CloneState(state)
	for _, id := range []sqltype.ID{sqltype.Name, sqltype.VarChar, sqltype.Int4, 13248, 13246, 13256, 13243} {
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}
		descriptor := *view.MustType(address)
		descriptor.SQLType, _ = foreign.CatalogRead().LookupType(id)
		replacement.SetDescriptor(address, &descriptor)
	}
	source := *in.Source
	source.Columns = append([]catalog.Column(nil), source.Columns...)
	for i := range source.Columns {
		domain, _ := foreign.CatalogRead().LookupType(udtUnitDomains[i])
		source.Columns[i].Usage = domain.Usage()
	}
	replacement.SetDescriptor(source.Address(), &source)
	// The zero-member composite has no dependency on the replaced INT4 generation.
	relation := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49100})
	relation.RelationKind = catalog.TableRelation
	replacement.SetDescriptor(relation.Address(), &relation)
	newer := udtUnitRows(t, udtUnitInput(t, f, f.e.Catalog.Read(replacement), f.owner), "empty")
	if newer[0][0].Type() == retained[0][0].Type() || retained[0][0].Type() != in.Source.Columns[0].Usage.Type {
		t.Fatal("domain generation retention")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bad := in
	bad.Context = ctx
	if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precancel", rows, err)
	}
	for _, remaining := range []int{1, 4, 100} {
		bad.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled capture", rows, err)
		}
	}
}
