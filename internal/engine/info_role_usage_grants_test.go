package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type roleUsageGrantsFixture struct {
	f     defaultUnitFixture
	state *catalog.State
	udt   *catalog.Type
}

func newRoleUsageGrantsFixture(t *testing.T) roleUsageGrantsFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	op, done := roleOperation(t, f.e, "postgres")
	defer done()
	domainUnitPublish(t, f.e, domainUnitStage(t, f.e, op, "CREATE DOMAIN public.ug_unit AS integer"))
	view := f.e.CatalogRead()
	state := catalog.CloneState(view.State())
	udt := *domainUnitType(t, view, "ug_unit")
	udt.ObjectNamespace, udt.Owner = f.schema, f.owner
	udt.ACL, udt.ACLSyntheticDefault = authorization.ACLValue{Explicit: true}, false
	state.SetDescriptor(udt.Address(), &udt)
	return roleUsageGrantsFixture{f, state, &udt}
}
func (f roleUsageGrantsFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoleUsageGrantsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engineCatalogRuntime{auth: auth, database: "unit_database"}}
}
func roleUsageGrantsRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoRoleUsageGrantsOID)
	if !ok {
		t.Fatal("missing role_usage_grants producer")
	}
	captured, err := captureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("anonymous source identity", captured)
	}
	for _, row := range captured.Anonymous {
		if len(row) != 8 {
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
func roleUsageGrantsBag(t *testing.T, rows [][]sqltype.Value, name string) map[[8]string]int {
	t.Helper()
	bag := make(map[[8]string]int)
	for _, row := range rows {
		var values [8]string
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
func roleUsageGrantsWant(grantor, grantee, privilege, option string) [8]string {
	return [8]string{grantor, grantee, "unit_database", "default_schema", "ug_unit", "DOMAIN", privilege, option}
}
func (f roleUsageGrantsFixture) check(t *testing.T, state *catalog.State, actor authorization.PrincipalRef, want ...[8]string) {
	t.Helper()
	f.checkType(t, state, actor, f.udt, want...)
}
func (f roleUsageGrantsFixture) checkType(t *testing.T, state *catalog.State, actor authorization.PrincipalRef, typ *catalog.Type, want ...[8]string) {
	t.Helper()
	bag := make(map[[8]string]int)
	for _, row := range want {
		row[4] = typ.Name()
		bag[row]++
	}
	got := roleUsageGrantsBag(t, roleUsageGrantsRows(t, f.input(t, state, actor)), typ.Name())
	if !reflect.DeepEqual(got, bag) {
		t.Fatalf("grants=%v want=%v", got, bag)
	}
}
func TestInfoRoleUsageGrantsMetadata(t *testing.T) {
	f := newRoleUsageGrantsFixture(t)
	in := f.input(t, f.state, f.f.reader)
	if rows := roleUsageGrantsRows(t, in); len(rows) != 0 {
		t.Fatal("reader should have nonnil empty rows", rows)
	}
	source := in.Source
	if source.Address().ObjectID != 16223 || source.Namespace().ObjectID != 13229 || source.RowType != 13482 || !source.VirtualView || source.RelationKind != catalog.CatalogRelation {
		t.Fatal("view identity", source)
	}
	names := [8]string{"grantor", "grantee", "object_catalog", "object_schema", "object_name", "object_type", "privilege_type", "is_grantable"}
	for i, column := range source.Columns {
		domain, base := sqltype.ID(13248), sqltype.Name
		if i == 5 || i == 6 {
			domain, base = 13246, sqltype.VarChar
		}
		if i >= 7 {
			domain, base = 13256, sqltype.VarChar
		}
		captured, ok := in.Catalog.LookupType(domain)
		if !ok || column.Name != names[i] || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16223 || column.OriginAttribute != int16(i+1) || column.DeclaredDimensions != 0 || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.Usage.Type != captured || column.Usage.Type.BaseType().ID() != base {
			t.Fatal("column contract", i, column)
		}
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13482})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13481 {
		t.Fatal("row backlink", row)
	}
	array, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13481})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("array backlink", array)
	}
}
