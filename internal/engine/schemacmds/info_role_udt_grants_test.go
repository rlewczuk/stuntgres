package schemacmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type roleUdtGrantsFixture struct {
	f     defaultUnitFixture
	state *catalog.State
	udt   *catalog.Type
}

func newRoleUdtGrantsFixture(t *testing.T) roleUdtGrantsFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	op, done := enginetest.RoleOperation(t, f.e, "postgres")
	defer done()
	domainUnitPublish(t, f.e, domainUnitStage(t, f.e, op, "CREATE TYPE public.ug_unit AS (a integer)"))
	view := f.e.CatalogRead()
	state := catalog.CloneState(view.State())
	udt := *domainUnitType(t, view, "ug_unit")
	udt.ObjectNamespace, udt.Owner = f.schema, f.owner
	udt.ACL, udt.ACLSyntheticDefault = authorization.ACLValue{Explicit: true}, false
	state.SetDescriptor(udt.Address(), &udt)
	return roleUdtGrantsFixture{f, state, &udt}
}
func (f roleUdtGrantsFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoleUdtGrantsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "unit_database"}}
}
func roleUdtGrantsRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoRoleUdtGrantsOID)
	if !ok {
		t.Fatal("missing role_udt_grants producer")
	}
	captured, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("anonymous source identity", captured)
	}
	for _, row := range captured.Anonymous {
		if len(row) != 7 {
			t.Fatal("projection width", row)
		}
		for i, value := range row {
			if value.IsNull() || value.Type() != in.Source.Columns[i].Usage.Type || !value.ValidResult(in.Source.Columns[i].Usage) {
				t.Fatal("source-owned domain carrier", i, value)
			}
			base, err := value.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			want := sqltype.Name
			if i >= 5 {
				want = sqltype.VarChar
			}
			authority, ok := in.Catalog.LookupType(want)
			if !ok || base.Type() != authority {
				t.Fatal("captured base authority", i, base)
			}
		}
	}
	return captured.Anonymous
}
func roleUdtGrantsBag(t *testing.T, rows [][]sqltype.Value, name string) map[[7]string]int {
	t.Helper()
	bag := make(map[[7]string]int)
	for _, row := range rows {
		var values [7]string
		for i, value := range row {
			base, err := value.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			values[i], err = base.Text()
			if err != nil {
				t.Fatal(err)
			}
		}
		if values[4] == name {
			bag[values]++
		}
	}
	return bag
}
func roleUdtGrantsWant(grantor, grantee, privilege, option string) [7]string {
	return [7]string{grantor, grantee, "unit_database", "default_schema", "ug_unit", privilege, option}
}
func (f roleUdtGrantsFixture) check(t *testing.T, state *catalog.State, actor authorization.PrincipalRef, want ...[7]string) {
	t.Helper()
	f.checkType(t, state, actor, f.udt, want...)
}
func (f roleUdtGrantsFixture) checkType(t *testing.T, state *catalog.State, actor authorization.PrincipalRef, typ *catalog.Type, want ...[7]string) {
	t.Helper()
	bag := make(map[[7]string]int)
	for _, row := range want {
		row[4] = typ.Name()
		bag[row]++
	}
	got := roleUdtGrantsBag(t, roleUdtGrantsRows(t, f.input(t, state, actor)), typ.Name())
	if !reflect.DeepEqual(got, bag) {
		t.Fatalf("grants=%v want=%v", got, bag)
	}
}
func TestInfoRoleUdtGrantsMetadata(t *testing.T) {
	f := newRoleUdtGrantsFixture(t)
	in := f.input(t, f.state, f.f.reader)
	if rows := roleUdtGrantsRows(t, in); len(rows) != 0 {
		t.Fatal("reader should have nonnil empty rows", rows)
	}
	source := in.Source
	if source.Address().ObjectID != 16221 || source.Namespace().ObjectID != 13229 || source.RowType != 13473 || !source.VirtualView || source.RelationKind != catalog.CatalogRelation {
		t.Fatal("view identity", source)
	}
	names := [7]string{"grantor", "grantee", "udt_catalog", "udt_schema", "udt_name", "privilege_type", "is_grantable"}
	for i, column := range source.Columns {
		domain, base := sqltype.ID(13248), sqltype.Name
		if i == 5 {
			domain, base = 13246, sqltype.VarChar
		}
		if i >= 6 {
			domain, base = 13256, sqltype.VarChar
		}
		captured, ok := in.Catalog.LookupType(domain)
		if !ok || column.Name != names[i] || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16221 || column.OriginAttribute != int16(i+1) || column.DeclaredDimensions != 0 || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.Usage.Type != captured || column.Usage.Type.BaseType().ID() != base {
			t.Fatal("column contract", i, column)
		}
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13473})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13472 {
		t.Fatal("row backlink", row)
	}
	array, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13472})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("array backlink", array)
	}
}

func newDefaultUnitFixture(t *testing.T) defaultUnitFixture {
	t.Helper()
	e := engine.New()
	roles := cloneRoleFixture(e.RoleStore.Published)
	refs := make([]authorization.PrincipalRef, 3)
	for i, name := range []string{"default_owner", "default_reader", "default_member"} {
		ref := e.RoleStore.Domain.Principal(uint64(20001 + i))
		refs[i] = ref
		putFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: name, Login: true, Inherit: true, ConnectionLimit: -1})

	}
	setFixtureEdges(roles, append(append([]authorization.Membership(nil), fixtureEdges(roles)...), authorization.Membership{Role: refs[0], Member: refs[2], Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Inherit: true, Set: true}))
	e.RoleStore.PublishLocked(roles)
	schema := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20010}
	next := catalog.CloneState(e.Catalog.State)
	namespace := &catalog.Namespace{ObjectAddress: schema, ObjectName: "default_schema", ObjectRevision: 2, Owner: refs[0]}
	next.SetDescriptor(schema, namespace)
	next.BindName(catalog.DescriptorNameKey(namespace), schema)
	e.Catalog.PublishLocked(next)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	return defaultUnitFixture{e: e, op: op, owner: refs[0], reader: refs[1], member: refs[2], schema: schema}
}

func dtpUnitAuthority(t *testing.T, f defaultUnitFixture, catalog catalog.Read) core.AuthorizationView {
	t.Helper()
	f.e.Mu.Lock()
	auth, err := f.e.AuthorizationLocked(f.op)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	auth.Recapture(catalog)
	return auth
}
