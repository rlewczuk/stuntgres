package enginetests_test

import (
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type roleTableGrantsFixture struct {
	f        defaultUnitFixture
	state    *catalog.State
	relation *catalog.Relation
}

func newRoleTableGrantsFixture(t *testing.T) roleTableGrantsFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20521}, ObjectNamespace: f.schema, ObjectName: "rtg_unit", ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}}
	state.SetDescriptor(relation.Address(), relation)
	return roleTableGrantsFixture{f, state, relation}
}
func (f roleTableGrantsFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoleTableGrantsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "unit_database"}}
}
func roleTableGrantsRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoRoleTableGrantsOID)
	if !ok {
		t.Fatal("missing role_table_grants producer")
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
func roleTableGrantsBag(t *testing.T, rows [][]sqltype.Value) map[[8]string]int {
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
		if values[4] == "rtg_unit" {
			bag[values]++
		}
	}
	return bag
}
func roleTableGrantsWant(grantor, grantee, privilege, option string) [8]string {
	hierarchy := "NO"
	if privilege == "SELECT" {
		hierarchy = "YES"
	}
	return [8]string{grantor, grantee, "unit_database", "default_schema", "rtg_unit", privilege, option, hierarchy}
}
func (f roleTableGrantsFixture) check(t *testing.T, state *catalog.State, actor authorization.PrincipalRef, want ...[8]string) {
	t.Helper()
	bag := make(map[[8]string]int)
	for _, row := range want {
		bag[row]++
	}
	got := roleTableGrantsBag(t, roleTableGrantsRows(t, f.input(t, state, actor)))
	if !reflect.DeepEqual(got, bag) {
		t.Fatalf("grants=%v want=%v", got, bag)
	}
}
func TestInfoRoleTableGrantsDescriptorAndDomains(t *testing.T) {
	f := newRoleTableGrantsFixture(t)
	in := f.input(t, f.state, f.f.reader)
	if rows := roleTableGrantsRows(t, in); len(rows) != 0 {
		t.Fatal("reader should have nonnil empty rows", rows)
	}
	source := in.Source
	if source.Address().ObjectID != 16218 || source.Namespace().ObjectID != 13229 || source.RowType != 13444 || !source.VirtualView || source.RelationKind != catalog.CatalogRelation {
		t.Fatal("view identity", source)
	}
	names := [8]string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "privilege_type", "is_grantable", "with_hierarchy"}
	for i, column := range source.Columns {
		domain, base := sqltype.ID(13248), sqltype.Name
		if i == 5 {
			domain, base = 13246, sqltype.VarChar
		}
		if i >= 6 {
			domain, base = 13256, sqltype.VarChar
		}
		captured, ok := in.Catalog.LookupType(domain)
		if !ok || column.Name != names[i] || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16218 || column.OriginAttribute != int16(i+1) || column.DeclaredDimensions != 0 || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.Usage.Type != captured || column.Usage.Type.BaseType().ID() != base {
			t.Fatal("column contract", i, column)
		}
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13444})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13443 {
		t.Fatal("row backlink", row)
	}
	array, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13443})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("array backlink", array)
	}
}

func TestInfoRoleTableGrantsOriginsAndVisibility(t *testing.T) {
	t.Run("absent_empty_seven_masks", func(t *testing.T) {
		f := newRoleTableGrantsFixture(t)
		f.check(t, f.state, f.f.owner)
		relation := *f.relation
		relation.Security.ACL = authorization.ACLValue{}
		f.state.SetDescriptor(relation.Address(), &relation)
		var baseline [][8]string
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
			baseline = append(baseline, roleTableGrantsWant("default_owner", "default_owner", privilege, "YES"))
		}
		f.check(t, f.state, f.f.owner, baseline...)
		relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.TableGrantPrivileges, authorization.UpdatePrivilege)}}
		f.state.SetDescriptor(relation.Address(), &relation)
		var want [][8]string
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
			option := "NO"
			if privilege == "UPDATE" {
				option = "YES"
			}
			want = append(want, roleTableGrantsWant("default_owner", "default_reader", privilege, option))
		}
		f.check(t, f.state, f.f.reader, want...)
	})
	t.Run("kinds_zero_width_no_column_expansion", func(t *testing.T) {
		for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation, catalog.CatalogRelation, catalog.SequenceRelation, catalog.MaterializedViewRelation, catalog.IndexRelation, catalog.CompositeRelation} {
			t.Run(fmt.Sprint(kind), func(t *testing.T) {
				f := newRoleTableGrantsFixture(t)
				relation := *f.relation
				relation.RelationKind = kind
				relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}
				relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UpdatePrivilege, 0)}}}
				f.state.SetDescriptor(relation.Address(), &relation)
				var want [][8]string
				switch kind {
				case catalog.TableRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation, catalog.CatalogRelation:
					want = [][8]string{roleTableGrantsWant("default_owner", "default_reader", "SELECT", "NO")}
				}
				f.check(t, f.state, f.f.reader, want...)
				relation.Security.ACL = authorization.ACLValue{Explicit: true}
				f.state.SetDescriptor(relation.Address(), &relation)
				f.check(t, f.state, f.f.reader)
			})
		}
	})
	t.Run("separate_origins_owner_authority", func(t *testing.T) {
		f := newRoleTableGrantsFixture(t)
		relation := *f.relation
		first := defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)
		second := first
		second.Grantor = f.f.member
		relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{first, second}}
		f.state.SetDescriptor(relation.Address(), &relation)
		a := roleTableGrantsWant("default_owner", "default_reader", "SELECT", "NO")
		b := roleTableGrantsWant("default_member", "default_reader", "SELECT", "NO")
		f.check(t, f.state, f.f.reader, a, b)
		f.check(t, f.state, f.f.owner, a)
		f.check(t, f.state, f.f.member, a, b)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.owner, Member: f.f.reader, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
		f.f.e.RoleStore.PublishLocked(roles)
		a[6], b[6] = "YES", "YES"
		f.check(t, f.state, f.f.reader, a, b)
		setOnly := cloneRoleFixture(roles)
		setFixtureEdges(setOnly, []authorization.Membership{{Role: f.f.owner, Member: f.f.reader, Grantor: f.f.e.Roles().LookupRef("postgres"), Set: true, Admin: true}})
		f.f.e.RoleStore.PublishLocked(setOnly)
		a[6], b[6] = "NO", "NO"
		f.check(t, f.state, f.f.reader, a, b)
		power := cloneRoleFixture(setOnly)
		reader := fixtureRole(power, f.f.reader)
		reader.Superuser = true
		putFixtureRole(power, reader)
		f.f.e.RoleStore.PublishLocked(power)
		a[6], b[6] = "YES", "YES"
		f.check(t, f.state, f.f.reader, a, b)
	})
	t.Run("public_and_superuser_attribute", func(t *testing.T) {
		f := newRoleTableGrantsFixture(t)
		relation := *f.relation
		relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
		f.state.SetDescriptor(relation.Address(), &relation)
		public := roleTableGrantsWant("default_owner", "PUBLIC", "SELECT", "NO")
		f.check(t, f.state, f.f.reader)
		f.check(t, f.state, f.f.owner, public)
		f.check(t, f.state, f.f.member, public)
		f.check(t, f.state, f.f.e.Roles().LookupRef("postgres"), public)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		power := fixtureRole(roles, f.f.reader)
		power.Superuser = true
		putFixtureRole(roles, power)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.reader, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
		f.f.e.RoleStore.PublishLocked(roles)
		f.check(t, f.state, f.f.member)
		f.check(t, f.state, f.f.reader, public)
	})
}
