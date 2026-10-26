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
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func schemataUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read, actor authorization.PrincipalRef, database string) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSchemataOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: database}}
}
func schemataUnitState(f defaultUnitFixture) *catalog.State {
	view := f.e.CatalogRead()
	state := catalog.CloneState(view.State())
	for address, d := range view.Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok {
			state.UnbindName(catalog.DescriptorNameKey(ns))
			if address.ObjectID != 13229 {
				state.RemoveDescriptor(address)
				continue
			}
			copy := *ns
			copy.ObjectName, copy.Owner, copy.ACL = "captured_schema", f.owner, authorization.ACLValue{Explicit: true}
			state.SetDescriptor(address, &copy)
			state.BindName(catalog.DescriptorNameKey(&copy), address)
		}
	}
	return state
}
func schemataUnitRows(t *testing.T, in pgcatalog.Input, want [][]any) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoSchemataOID)
	if !ok {
		t.Fatal("missing schemata producer")
	}
	capture, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	if capture.Ref != in.Ref || capture.View != nil || capture.Anonymous == nil {
		t.Fatal("anonymous source", capture)
	}
	got := make([][]any, 0, len(capture.Anonymous))
	for _, row := range capture.Anonymous {
		if len(row) != 7 {
			t.Fatal("row width", len(row))
		}
		cells := make([]any, 7)
		for i, v := range row {
			if v.Type() != in.Source.Columns[i].Usage.Type || !v.ValidResult(in.Source.Columns[i].Usage) || v.IsNull() != (i >= 3) {
				t.Fatal("owned nullable domain", i, v)
			}
			if i < 3 {
				cells[i] = infoUserMappingsUnitText(t, v)
			}
		}
		got = append(got, cells)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows=%#v want=%#v", got, want)
	}
	return capture.Anonymous
}
func schemataUnitWant(database, schema, owner string) [][]any {
	return [][]any{{database, schema, owner, nil, nil, nil, nil}}
}

func TestInfoSchemataProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.Catalog.Read(schemataUnitState(f))
	in := schemataUnitInput(t, f, view, f.owner, "MiXeD db")
	source := in.Source
	if pgcatalog.InfoSchemataOID != 16230 || source.Name() != "schemata" || source.Namespace().ObjectID != 13229 || !source.VirtualView || source.RelationKind != catalog.CatalogRelation || source.RowType != 13405 {
		t.Fatal("source identity", source)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13405})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13404 {
		t.Fatal("row identity", row)
	}
	array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13404})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("array identity", array)
	}
	names := []string{"catalog_name", "schema_name", "schema_owner", "default_character_set_catalog", "default_character_set_schema", "default_character_set_name", "sql_path"}
	if len(source.Columns) != 7 || len(source.ColumnMeta.CatalogAttributes) != 7 {
		t.Fatal("column width")
	}
	for i, col := range source.Columns {
		domainID, baseID := sqltype.ID(13248), sqltype.Name
		if i == 6 {
			domainID, baseID = 13246, sqltype.VarChar
		}
		domain, ok := view.LookupType(domainID)
		base, present := view.LookupType(baseID)
		if !ok || !present || col.Name != names[i] || col.Usage != domain.Usage() || !domain.IsDomain() || domain.BaseType() != base || !col.Nullable || !col.NullabilityKnown || col.OriginTable != 16230 || col.OriginAttribute != int16(i+1) || col.Usage.Typmod != -1 || col.Usage.Collation.ID != 950 || col.DeclaredDimensions != 0 {
			t.Fatal("column domain/origin", i, col)
		}
		attr := source.ColumnMeta.CatalogAttributes[i]
		if attr.NotNull || attr.Collation != 950 || attr.Dimensions != 0 {
			t.Fatal("attribute", attr)
		}
	}
	first := schemataUnitRows(t, in, schemataUnitWant("MiXeD db", "captured_schema", "default_owner"))
	second := schemataUnitRows(t, in, schemataUnitWant("MiXeD db", "captured_schema", "default_owner"))
	first[0][0] = sqltype.Null(first[0][0].Type())
	if second[0][0].IsNull() {
		t.Fatal("captures share cell backing")
	}
	schemataUnitRows(t, schemataUnitInput(t, f, view, f.owner, ""), schemataUnitWant("", "captured_schema", "default_owner"))
	schemataUnitRows(t, schemataUnitInput(t, f, view, f.reader, "MiXeD db"), [][]any{})
}

func TestInfoSchemataCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := schemataUnitState(f)
	view := f.e.Catalog.Read(state)
	in := schemataUnitInput(t, f, view, f.owner, "MiXeD db")
	retained := schemataUnitRows(t, in, schemataUnitWant("MiXeD db", "captured_schema", "default_owner"))
	ns := view.MustNamespace(in.Source.Namespace())
	changed := catalog.CloneState(state)
	copy := *ns
	copy.ObjectName = "renamed_schema"
	copy.Owner = f.reader
	changed.UnbindName(catalog.DescriptorNameKey(ns))
	changed.SetDescriptor(copy.Address(), &copy)
	changed.BindName(catalog.DescriptorNameKey(&copy), copy.Address())
	schemataUnitRows(t, schemataUnitInput(t, f, f.e.Catalog.Read(changed), f.reader, "MiXeD db"), schemataUnitWant("MiXeD db", "renamed_schema", "default_reader"))
	granted := copy
	granted.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.reader, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.CreatePrivilege}}}
	changed.SetDescriptor(granted.Address(), &granted)
	schemataUnitRows(t, schemataUnitInput(t, f, f.e.Catalog.Read(changed), f.owner, "MiXeD db"), schemataUnitWant("MiXeD db", "renamed_schema", "default_reader"))
	roles := cloneRoleFixture(f.e.RoleStore.Published)
	owner := fixtureRole(roles, f.owner)
	owner.Name = "renamed_owner"
	putFixtureRole(roles, owner)
	f.e.RoleStore.PublishLocked(roles)
	schemataUnitRows(t, schemataUnitInput(t, f, view, f.member, "MiXeD db"), schemataUnitWant("MiXeD db", "captured_schema", "renamed_owner"))
	roles = cloneRoleFixture(roles)
	setFixtureEdges(roles, nil)
	f.e.RoleStore.PublishLocked(roles)
	schemataUnitRows(t, schemataUnitInput(t, f, view, f.member, "MiXeD db"), [][]any{})
	schemataUnitRows(t, in, schemataUnitWant("MiXeD db", "captured_schema", "default_owner"))
	if infoUserMappingsUnitText(t, retained[0][1]) != "captured_schema" || infoUserMappingsUnitText(t, retained[0][2]) != "default_owner" {
		t.Fatal("retained rows changed")
	}
	// Implicit database ownership supplies owner authority even with an empty ACL.
	implicit := catalog.CloneState(state)
	database, _ := view.Database(catalog.CurrentDatabaseAddress)
	db := *database
	db.Owner = f.reader
	implicit.SetDescriptor(db.Address(), &db)
	implicitNS := *ns
	implicitNS.Owner = f.e.Roles().LookupRef("pg_database_owner")
	implicit.SetDescriptor(ns.Address(), &implicitNS)
	schemataUnitRows(t, schemataUnitInput(t, f, f.e.Catalog.Read(implicit), f.reader, "MiXeD db"), schemataUnitWant("MiXeD db", "captured_schema", "pg_database_owner"))
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSchemataOID)
	foreign := engine.New()
	// Install a coherent replacement generation into this catalog and source.
	replacement := catalog.CloneState(state)
	for _, id := range []sqltype.ID{sqltype.Name, sqltype.VarChar, 13248, 13246} {
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}
		descriptor := *view.MustType(address)
		descriptor.SQLType, _ = foreign.CatalogRead().LookupType(id)
		replacement.SetDescriptor(address, &descriptor)
	}
	replacedSource := *in.Source
	replacedSource.Columns = append([]catalog.Column(nil), in.Source.Columns...)
	for i := range replacedSource.Columns {
		id := sqltype.ID(13248)
		if i == 6 {
			id = 13246
		}
		typ, _ := foreign.CatalogRead().LookupType(id)
		replacedSource.Columns[i].Usage = typ.Usage()
	}
	replacement.SetDescriptor(replacedSource.Address(), &replacedSource)
	replacedRows := schemataUnitRows(t, schemataUnitInput(t, f, f.e.Catalog.Read(replacement), f.owner, "MiXeD db"), schemataUnitWant("MiXeD db", "captured_schema", "renamed_owner"))
	if replacedRows[0][0].Type() == retained[0][0].Type() || retained[0][0].Type() != in.Source.Columns[0].Usage.Type {
		t.Fatal("domain generation retention")
	}
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
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
	for _, malformed := range []string{"width", "database", "primitive", "foreign_domain", "typmod", "collation", "foreign_name_base", "foreign_varchar_base", "namespace_address", "namespace_binding", "missing_owner", "foreign_owner", "stale_owner"} {
		for column := range 7 {
			bad := catalog.CloneState(state)
			source := *in.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			switch malformed {
			case "width":
				source.Columns = source.Columns[:6]
			case "database":
				bad.RemoveDescriptor(catalog.CurrentDatabaseAddress)
			case "primitive":
				source.Columns[column].Usage.Type = source.Columns[column].Usage.Type.BaseType()
			case "foreign_domain":
				source.Columns[column].Usage.Type, _ = foreign.CatalogRead().LookupType(source.Columns[column].Usage.Type.ID())
			case "typmod":
				source.Columns[column].Usage.Typmod = 3
			case "collation":
				source.Columns[column].Usage.Collation.ID = 0
			case "foreign_name_base", "foreign_varchar_base":
				id := sqltype.Name
				if malformed == "foreign_varchar_base" {
					id = sqltype.VarChar
				}
				address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}
				descriptor := *view.MustType(address)
				descriptor.SQLType, _ = foreign.CatalogRead().LookupType(id)
				bad.SetDescriptor(address, &descriptor)
			default:
				copy := *ns
				switch malformed {
				case "namespace_address":
					copy.ObjectAddress.ObjectID++
				case "namespace_binding":
					copy.ObjectName = "unbound"
				case "missing_owner":
					copy.Owner = authorization.PrincipalRef{}
				case "foreign_owner":
					copy.Owner = foreign.Roles().LookupRef("postgres")
				case "stale_owner":
					copy.Owner = f.e.RoleStore.Domain.Principal(999999)
				}
				bad.SetDescriptor(ns.Address(), &copy)
			}
			bad.SetDescriptor(source.Address(), &source)
			rows, err := produce(schemataUnitInput(t, f, f.e.Catalog.Read(bad), f.owner, "MiXeD db"))
			schemataUnitFailure(t, rows, err)
			if malformed != "primitive" && malformed != "foreign_domain" && malformed != "typmod" && malformed != "collation" {
				break
			}
		}
	}
}
func schemataUnitFailure(t *testing.T, rows [][]sqltype.Value, err error) {
	t.Helper()
	var state interface{ SQLState() string }
	if rows != nil || err == nil || errors.As(err, &state) {
		t.Fatal("invalid capture must return nil rows and ordinary error", rows, err)
	}
}
func TestInfoSchemataCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.Catalog.Read(schemataUnitState(f))
	in := schemataUnitInput(t, f, view, f.owner, "MiXeD db")
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSchemataOID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bad := in
	bad.Context = ctx
	if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precancel", rows, err)
	}
	// Entry, early/mid enumeration and final publication edges are deterministic.
	count := 0
	for range view.Descriptors() {
		count++
	}
	for _, remaining := range []int{1, 4, count / 2, count + 2} {
		bad.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", remaining, rows, err)
		}
	}
	schemataUnitRows(t, in, schemataUnitWant("MiXeD db", "captured_schema", "default_owner"))
}
