package enginetests_test

import (
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// Use the existing real column-ACL fixture, including its sparse attributes.
func (f roleColumnGrantsFixture) columnInput(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	in := f.input(t, state, actor)
	in.Source = in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnPrivilegesOID})
	in.Ref = in.Owner.Domain.Ref(in.Catalog.Epoch(), in.Source.Address())
	return in
}

func columnPrivilegesRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoColumnPrivilegesOID)
	if !ok {
		t.Fatal("missing column_privileges producer")
	}
	captured, err := catalogrt.CaptureRows(in, produce)
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

func (f roleColumnGrantsFixture) columnCheck(t *testing.T, state *catalog.State, actor authorization.PrincipalRef, want ...[8]string) {
	t.Helper()
	bag := make(map[[8]string]int)
	for _, row := range want {
		bag[row]++
	}
	got := roleColumnGrantsBag(t, columnPrivilegesRows(t, f.columnInput(t, state, actor)))
	if !reflect.DeepEqual(got, bag) {
		t.Fatalf("column privileges=%v want=%v", got, bag)
	}
}

func TestInfoColumnPrivilegesProducer(t *testing.T) {
	f := newRoleColumnGrantsFixture(t)
	in := f.columnInput(t, f.state, f.f.reader)
	f.columnCheck(t, f.state, f.f.reader)
	source := in.Source
	if pgcatalog.InfoColumnPrivilegesOID != 16259 || source.Name() != "column_privileges" || source.Namespace().ObjectID != 13229 || source.RowType != 13309 || !source.VirtualView || source.RelationKind != catalog.CatalogRelation || len(source.Columns) != 8 || pgcatalog.ClassKind(in.Catalog, source) != "v" {
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
		ownedDomain, _ := in.Catalog.LookupType(domain)
		ownedBase, _ := in.Catalog.LookupType(base)
		if column.Name != names[i] || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16259 || column.OriginAttribute != int16(i+1) || column.DeclaredDimensions != 0 || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.Usage.Type != ownedDomain || column.Usage.Type.BaseType() != ownedBase {
			t.Fatal("column contract", i, column)
		}
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13309})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13308 {
		t.Fatal("row backlink", row)
	}
	array, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13308})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("array backlink", array)
	}
	if pgcatalog.BuiltinViewEvents(pgcatalog.InfoColumnPrivilegesOID, nil) != 0 {
		t.Fatal("read-only view acquired writable events")
	}
}

func TestInfoColumnPrivilegesSemantics(t *testing.T) {
	t.Run("raw_union_owner_and_masks", func(t *testing.T) {
		f := newRoleColumnGrantsFixture(t)
		relation := *f.relation
		entry := defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)
		relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}
		relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{entry}}}
		f.state.SetDescriptor(relation.Address(), &relation)
		a := roleColumnGrantsWant("default_owner", "default_reader", "a", "SELECT", "NO")
		b := roleColumnGrantsWant("default_owner", "default_reader", "b", "SELECT", "NO")
		f.columnCheck(t, f.state, f.f.reader, a, b)
		option := entry
		option.GrantOptions = authorization.SelectPrivilege
		relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{option}}}
		f.state.SetDescriptor(relation.Address(), &relation)
		yes := a
		yes[7] = "YES"
		f.columnCheck(t, f.state, f.f.reader, a, yes, b)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		setFixtureEdges(roles, append(fixtureEdges(roles), authorization.Membership{Role: f.f.owner, Member: f.f.reader, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}))
		f.f.e.RoleStore.PublishLocked(roles)
		b[7] = "YES"
		f.columnCheck(t, f.state, f.f.reader, yes, yes, b)

		// An absent ACL supplies owner defaults; explicit-empty supplies none.
		relation.Security = catalog.RelationSecurity{}
		f.state.SetDescriptor(relation.Address(), &relation)
		var baseline [][8]string
		for _, column := range []string{"a", "b"} {
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
				baseline = append(baseline, roleColumnGrantsWant("default_owner", "default_owner", column, privilege, "YES"))
			}
		}
		f.columnCheck(t, f.state, f.f.owner, baseline...)
		relation.Security.ACL = authorization.ACLValue{Explicit: true}
		f.state.SetDescriptor(relation.Address(), &relation)
		f.columnCheck(t, f.state, f.f.owner)
		// Every column-capable mask survives, while DELETE and other bits do not.
		relation.Security.ACL.Entries = []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.TableGrantPrivileges, 0)}
		f.state.SetDescriptor(relation.Address(), &relation)
		var want [][8]string
		for _, column := range []string{"a", "b"} {
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
				want = append(want, roleColumnGrantsWant("default_owner", "default_reader", column, privilege, "YES"))
			}
		}
		f.columnCheck(t, f.state, f.f.reader, want...)
	})
	t.Run("public_name_identity_and_owner_closure", func(t *testing.T) {
		f := newRoleColumnGrantsFixture(t)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		named := fixtureRole(roles, f.f.reader)
		named.Name = "PUBLIC"
		putFixtureRole(roles, named)
		setFixtureEdges(roles, nil)
		f.f.e.RoleStore.PublishLocked(roles)
		relation := *f.relation
		pseudo := authorization.ACLEntry{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}
		namedEntry := defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, authorization.SelectPrivilege)
		relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{pseudo, namedEntry}}}
		f.state.SetDescriptor(relation.Address(), &relation)
		no := roleColumnGrantsWant("default_owner", "PUBLIC", "a", "SELECT", "NO")
		yes := no
		yes[7] = "YES"
		f.columnCheck(t, f.state, f.f.member, no, yes)
		f.check(t, f.state, f.f.member)
		f.check(t, f.state, f.f.owner, no, yes)
		// Visibility from the name term must be established before owner closure.
		namedEntry.GrantOptions = 0
		relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{pseudo, namedEntry}}}
		f.state.SetDescriptor(relation.Address(), &relation)
		f.columnCheck(t, f.state, f.f.member, no, no)
		roles = cloneRoleFixture(roles)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.owner, Member: f.f.reader, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
		f.f.e.RoleStore.PublishLocked(roles)
		f.columnCheck(t, f.state, f.f.member, no, yes)
		f.check(t, f.state, f.f.member)
	})
	t.Run("classes_sparse_and_empty", func(t *testing.T) {
		for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation, catalog.CatalogRelation, catalog.SequenceRelation, catalog.MaterializedViewRelation, catalog.IndexRelation, catalog.CompositeRelation} {
			t.Run(fmt.Sprint(kind), func(t *testing.T) {
				f := newRoleColumnGrantsFixture(t)
				relation := *f.relation
				relation.RelationKind = kind
				relation.Attributes = []int32{-1, 1, 3}
				relation.Columns = append([]catalog.Column{{Name: "system", Usage: relation.Columns[0].Usage}}, relation.Columns...)
				relation.Security.ColumnACL = map[int32]authorization.ACLValue{
					-1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}},
					1:  {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}},
					2:  {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}},
					3:  {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UpdatePrivilege, 0)}},
				}
				f.state.SetDescriptor(relation.Address(), &relation)
				var want [][8]string
				switch kind {
				case catalog.TableRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation, catalog.CatalogRelation:
					want = [][8]string{roleColumnGrantsWant("default_owner", "default_reader", "a", "SELECT", "NO"), roleColumnGrantsWant("default_owner", "default_reader", "b", "UPDATE", "NO")}
				}
				f.columnCheck(t, f.state, f.f.reader, want...)
				relation.Columns, relation.Attributes = nil, nil
				f.state.SetDescriptor(relation.Address(), &relation)
				f.columnCheck(t, f.state, f.f.reader)
			})
		}
	})
}
