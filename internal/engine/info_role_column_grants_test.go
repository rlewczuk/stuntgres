package engine

import (
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type roleColumnGrantsFixture struct {
	f        defaultUnitFixture
	state    *catalog.State
	relation *catalog.Relation
}

func newRoleColumnGrantsFixture(t *testing.T) roleColumnGrantsFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.catalog.State)
	typ, _ := f.e.CatalogRead().LookupType(sqltype.Int4)
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20520}, ObjectNamespace: f.schema, ObjectName: "rcg_unit", ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Attributes: []int32{1, 3}, Columns: []catalog.Column{{Name: "a", Usage: typ.Usage()}, {Name: "b", Usage: typ.Usage()}}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}}
	state.SetDescriptor(relation.Address(), relation)
	return roleColumnGrantsFixture{f, state, relation}
}
func (f roleColumnGrantsFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoleColumnGrantsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engineCatalogRuntime{auth: auth, database: "unit_database"}}
}
func roleColumnGrantsRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoRoleColumnGrantsOID)
	if !ok {
		t.Fatal("missing role_column_grants producer")
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
		}
	}
	return captured.Anonymous
}
func roleColumnGrantsBag(t *testing.T, rows [][]sqltype.Value) map[[8]string]int {
	t.Helper()
	bag := make(map[[8]string]int)
	for _, row := range rows {
		var values [8]string
		for i, value := range row {
			base, err := value.BaseValue()
			if err != nil {
				t.Fatal("domain base value", i, err)
			}
			wantBase := sqltype.Name
			if i >= 6 {
				wantBase = sqltype.VarChar
			}
			if base.IsNull() || base.Type() == nil || base.Type().ID() != wantBase {
				t.Fatal("incorrect grant column base carrier", i, base)
			}
			values[i], err = base.Text()
			if err != nil {
				t.Fatal("grant column text", i, err)
			}
		}
		if values[4] == "rcg_unit" {
			bag[values]++
		}
	}
	return bag
}
func roleColumnGrantsWant(grantor, grantee, column, privilege, option string) [8]string {
	return [8]string{grantor, grantee, "unit_database", "default_schema", "rcg_unit", column, privilege, option}
}
func (f roleColumnGrantsFixture) check(t *testing.T, state *catalog.State, actor authorization.PrincipalRef, want ...[8]string) {
	t.Helper()
	bag := make(map[[8]string]int)
	for _, row := range want {
		bag[row]++
	}
	got := roleColumnGrantsBag(t, roleColumnGrantsRows(t, f.input(t, state, actor)))
	if !reflect.DeepEqual(got, bag) {
		t.Fatalf("grants=%v want=%v", got, bag)
	}
}
func TestInfoRoleColumnGrantsProducer(t *testing.T) {
	f := newRoleColumnGrantsFixture(t)
	in := f.input(t, f.state, f.f.reader)
	rows := roleColumnGrantsRows(t, in)
	if len(rows) != 0 {
		t.Fatal("reader should have successful nonnil empty rows", rows)
	}
	source := in.Source
	if source.Address().ObjectID != 16213 || source.Namespace().ObjectID != 13229 || source.RowType != 13367 || !source.VirtualView || source.RelationKind != catalog.CatalogRelation {
		t.Fatal("view identity", source)
	}
	names := [8]string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "column_name", "privilege_type", "is_grantable"}
	for i, column := range source.Columns {
		domain, base := sqltype.ID(13248), sqltype.Name
		if i == 6 {
			domain, base = 13246, sqltype.VarChar
		}
		if i == 7 {
			domain, base = 13256, sqltype.VarChar
		}
		if column.Name != names[i] || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16213 || column.OriginAttribute != int16(i+1) || column.DeclaredDimensions != 0 || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.Usage.Type.ID() != domain || column.Usage.Type.BaseType().ID() != base {
			t.Fatal("column contract", i, column)
		}
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13367})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13366 {
		t.Fatal("row backlink", row)
	}
	array, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13366})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("array backlink", array)
	}
}

func TestInfoRoleColumnGrantsSemantics(t *testing.T) {
	t.Run("raw_union_and_owner", func(t *testing.T) {
		f := newRoleColumnGrantsFixture(t)
		relation := *f.relation
		relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}
		relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}}
		f.state.SetDescriptor(relation.Address(), &relation)
		a := roleColumnGrantsWant("default_owner", "default_reader", "a", "SELECT", "NO")
		b := roleColumnGrantsWant("default_owner", "default_reader", "b", "SELECT", "NO")
		f.check(t, f.state, f.f.reader, a, b)
		relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, authorization.SelectPrivilege)}}}
		f.state.SetDescriptor(relation.Address(), &relation)
		yes := a
		yes[7] = "YES"
		f.check(t, f.state, f.f.reader, a, yes, b)
		roles := cloneRoleFixture(f.f.e.roles.published)
		setFixtureEdges(roles, append(fixtureEdges(roles), authorization.Membership{Role: f.f.owner, Member: f.f.reader, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}))
		f.f.e.roles.published = roles
		b[7] = "YES"
		f.check(t, f.state, f.f.reader, yes, yes, b)
		// The owner sees its reader grants through the enabled-grantor side.
		// Its implicit options cannot invent any owner-to-owner origin on
		// this explicit ACL; the full bag still contains only reader origins.
		f.check(t, f.state, f.f.owner, yes, yes, b)
	})
	t.Run("absent_empty_four_masks", func(t *testing.T) {
		f := newRoleColumnGrantsFixture(t)
		f.check(t, f.state, f.f.owner)
		relation := *f.relation
		relation.Security.ACL = authorization.ACLValue{}
		f.state.SetDescriptor(relation.Address(), &relation)
		var baseline [][8]string
		for _, column := range []string{"a", "b"} {
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
				baseline = append(baseline, roleColumnGrantsWant("default_owner", "default_owner", column, privilege, "YES"))
			}
		}
		f.check(t, f.state, f.f.owner, baseline...)
		relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.TableGrantPrivileges, authorization.UpdatePrivilege)}}
		f.state.SetDescriptor(relation.Address(), &relation)
		var want [][8]string
		for _, column := range []string{"a", "b"} {
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
				option := "NO"
				if privilege == "UPDATE" {
					option = "YES"
				}
				want = append(want, roleColumnGrantsWant("default_owner", "default_reader", column, privilege, option))
			}
		}
		f.check(t, f.state, f.f.reader, want...)
	})
	t.Run("kinds_and_sparse_attributes", func(t *testing.T) {
		for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation, catalog.CatalogRelation, catalog.SequenceRelation, catalog.MaterializedViewRelation, catalog.IndexRelation, catalog.CompositeRelation} {
			t.Run(fmt.Sprint(kind), func(t *testing.T) {
				f := newRoleColumnGrantsFixture(t)
				relation := *f.relation
				relation.RelationKind = kind
				// Attribute 2 has been dropped; a system attribute is never emitted.
				relation.Attributes = []int32{-1, 1, 3}
				relation.Columns = append([]catalog.Column{{Name: "system", Usage: relation.Columns[0].Usage}}, relation.Columns...)
				relation.Security.ColumnACL = map[int32]authorization.ACLValue{
					1:  {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}},
					2:  {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}},
					3:  {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UpdatePrivilege, 0)}},
					-1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}},
				}
				f.state.SetDescriptor(relation.Address(), &relation)
				var want [][8]string
				switch kind {
				case catalog.TableRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation, catalog.CatalogRelation:
					want = [][8]string{roleColumnGrantsWant("default_owner", "default_reader", "a", "SELECT", "NO"), roleColumnGrantsWant("default_owner", "default_reader", "b", "UPDATE", "NO")}
				}
				f.check(t, f.state, f.f.reader, want...)
				relation.Attributes = nil
				relation.Columns = nil
				f.state.SetDescriptor(relation.Address(), &relation)
				f.check(t, f.state, f.f.reader)
			})
		}
	})
	t.Run("enabled_sides_and_distinct_grantors", func(t *testing.T) {
		f := newRoleColumnGrantsFixture(t)
		relation := *f.relation
		first := defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)
		second := first
		second.Grantor = f.f.member
		relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{first, second}}}
		f.state.SetDescriptor(relation.Address(), &relation)
		a := roleColumnGrantsWant("default_owner", "default_reader", "a", "SELECT", "NO")
		b := roleColumnGrantsWant("default_member", "default_reader", "a", "SELECT", "NO")
		f.check(t, f.state, f.f.reader, a, b)
		f.check(t, f.state, f.f.owner, a)
		f.check(t, f.state, f.f.member, a, b)
		roles := cloneRoleFixture(f.f.e.roles.published)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.owner, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Set: true, Admin: true}})
		f.f.e.roles.published = roles
		f.check(t, f.state, f.f.member, b)
		setOnly := cloneRoleFixture(roles)
		setFixtureEdges(setOnly, []authorization.Membership{{Role: f.f.reader, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Set: true, Admin: true}})
		f.f.e.roles.published = setOnly
		f.check(t, f.state, f.f.member, b)
	})
	t.Run("public_and_superuser_attribute", func(t *testing.T) {
		f := newRoleColumnGrantsFixture(t)
		relation := *f.relation
		relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}}
		f.state.SetDescriptor(relation.Address(), &relation)
		public := roleColumnGrantsWant("default_owner", "PUBLIC", "a", "SELECT", "NO")
		f.check(t, f.state, f.f.reader)
		f.check(t, f.state, f.f.owner, public)
		f.check(t, f.state, f.f.member, public)
		f.check(t, f.state, f.f.e.Roles().LookupRef("postgres"), public)
		roles := cloneRoleFixture(f.f.e.roles.published)
		power := fixtureRole(roles, f.f.reader)
		power.Superuser = true
		putFixtureRole(roles, power)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.reader, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
		f.f.e.roles.published = roles
		f.check(t, f.state, f.f.member)
		f.check(t, f.state, f.f.reader, public)
	})
}
