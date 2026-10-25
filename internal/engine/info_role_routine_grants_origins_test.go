package engine

import (
	"fmt"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
)

func TestInfoRoleRoutineGrantsOrigins(t *testing.T) {
	testRoutinePrivilegesOrigins(t, newRoleRoutineGrantsFixture, false)
}

func testRoutinePrivilegesOrigins(t *testing.T, newFixture func(*testing.T) roleRoutineGrantsFixture, includePublic bool) {
	t.Helper()
	t.Run("defaults_empty_masks", func(t *testing.T) {
		f := newFixture(t)
		f.check(t, f.state, f.f.owner)
		routine := *f.routine
		routine.ACL = authorization.ACLValue{}
		f.state.SetDescriptor(routine.Address(), &routine)
		owner := roleRoutineGrantsWant("default_owner", "default_owner", "EXECUTE", "YES")
		public := roleRoutineGrantsWant("default_owner", "PUBLIC", "EXECUTE", "NO")
		f.check(t, f.state, f.f.owner, owner, public)
		f.check(t, f.state, f.f.member, owner, public)
		if includePublic {
			f.check(t, f.state, f.f.reader, public)
		} else {
			f.check(t, f.state, f.f.reader)
		}
		f.check(t, f.state, f.f.e.Roles().LookupRef("postgres"), owner, public)
		for _, mask := range []authorization.Privileges{0, authorization.SelectPrivilege, authorization.UpdatePrivilege, authorization.UsagePrivilege, authorization.ExecutePrivilege, authorization.ExecutePrivilege | authorization.SelectPrivilege} {
			routine.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, mask, authorization.ExecutePrivilege)}}
			f.state.SetDescriptor(routine.Address(), &routine)
			var want [][10]string
			if mask&authorization.ExecutePrivilege != 0 {
				want = append(want, roleRoutineGrantsWant("default_owner", "default_reader", "EXECUTE", "YES"))
			}
			f.check(t, f.state, f.f.reader, want...)
		}
		routine.ACL = authorization.ACLValue{Explicit: true}
		f.state.SetDescriptor(routine.Address(), &routine)
		f.check(t, f.state, f.f.owner)
		f.check(t, f.state, f.f.e.Roles().LookupRef("postgres"))
	})
	t.Run("kinds_body_provider_independence", func(t *testing.T) {
		for _, kind := range []catalog.RoutineKind{catalog.FunctionRoutine, catalog.ProcedureRoutine, catalog.AggregateRoutine, catalog.WindowRoutine} {
			for _, provider := range []string{"plain", "sql_body", "builtin", "extension"} {
				t.Run(fmt.Sprintf("%c/%s", kind, provider), func(t *testing.T) {
					f := newFixture(t)
					routine := *f.routine
					routine.RoutineKind = kind
					routine.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.ExecutePrivilege, 0)}}
					switch provider {
					case "sql_body":
						routine.SQL = &proc.Definition{Standard: true}
						routine.SQLBody = stored.NewRoutineProgram(nil)
					case "builtin":
						routine.Builtin = &catalog.BuiltinRoutineDeclaration{Identity: catalog.RoutineIdentity{ID: routine.Address().ObjectID, Namespace: f.f.schema.ObjectID, Name: routine.Name()}, Kind: kind}
					case "extension":
						extension := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 20523}, ObjectName: "rrg_extension", ObjectRevision: 1, Owner: f.f.owner, TargetNamespace: f.f.schema, Members: []ids.ObjectAddress{routine.Address()}}
						f.state.SetDescriptor(extension.Address(), extension)
						routine.Extension = extension.Address()
					}
					f.state.SetDescriptor(routine.Address(), &routine)
					f.check(t, f.state, f.f.reader, roleRoutineGrantsWant("default_owner", "default_reader", "EXECUTE", "NO"))
				})
			}
		}
	})
	t.Run("separate_origins_grantee_ownership", func(t *testing.T) {
		f := newFixture(t)
		routine := *f.routine
		first := defaultUnitReaderEntry(f.f, authorization.ExecutePrivilege, 0)
		second := first
		second.Grantor = f.f.member
		routine.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{first, second}}
		f.state.SetDescriptor(routine.Address(), &routine)
		a := roleRoutineGrantsWant("default_owner", "default_reader", "EXECUTE", "NO")
		b := roleRoutineGrantsWant("default_member", "default_reader", "EXECUTE", "NO")
		f.check(t, f.state, f.f.reader, a, b)
		f.check(t, f.state, f.f.owner, a)
		f.check(t, f.state, f.f.member, a, b)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.owner, Member: f.f.reader, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
		f.f.e.RoleStore.PublishLocked(roles)
		a[9], b[9] = "YES", "YES"
		f.check(t, f.state, f.f.reader, a, b)
		setOnly := cloneRoleFixture(roles)
		setFixtureEdges(setOnly, []authorization.Membership{{Role: f.f.owner, Member: f.f.reader, Grantor: f.f.e.Roles().LookupRef("postgres"), Set: true, Admin: true}})
		f.f.e.RoleStore.PublishLocked(setOnly)
		a[9], b[9] = "NO", "NO"
		f.check(t, f.state, f.f.reader, a, b)
		power := cloneRoleFixture(setOnly)
		reader := fixtureRole(power, f.f.reader)
		reader.Superuser = true
		putFixtureRole(power, reader)
		f.f.e.RoleStore.PublishLocked(power)
		a[9], b[9] = "YES", "YES"
		f.check(t, f.state, f.f.reader, a, b)
	})
	t.Run("public_actual_superuser", func(t *testing.T) {
		f := newFixture(t)
		routine := *f.routine
		routine.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege}}}
		f.state.SetDescriptor(routine.Address(), &routine)
		public := roleRoutineGrantsWant("default_owner", "PUBLIC", "EXECUTE", "NO")
		if includePublic {
			f.check(t, f.state, f.f.reader, public)
		} else {
			f.check(t, f.state, f.f.reader)
		}
		f.check(t, f.state, f.f.owner, public)
		f.check(t, f.state, f.f.member, public)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		power := fixtureRole(roles, f.f.reader)
		power.Superuser = true
		putFixtureRole(roles, power)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.reader, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
		f.f.e.RoleStore.PublishLocked(roles)
		if includePublic {
			f.check(t, f.state, f.f.member, public)
		} else {
			f.check(t, f.state, f.f.member)
		}
		f.check(t, f.state, f.f.reader, public)
	})
	t.Run("recursive_multiple_paths", func(t *testing.T) {
		f := newFixture(t)
		routine := *f.routine
		routine.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.ExecutePrivilege, 0)}}
		f.state.SetDescriptor(routine.Address(), &routine)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		root := f.f.e.Roles().LookupRef("postgres")
		x, y := f.f.e.RoleStore.Domain.Principal(20531), f.f.e.RoleStore.Domain.Principal(20532)
		for i, ref := range []authorization.PrincipalRef{x, y} {
			putFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: fmt.Sprint("rrg_path", i), Inherit: true, ConnectionLimit: -1})
		}
		edges := []authorization.Membership{{Role: f.f.reader, Member: x, Grantor: root, Inherit: true}, {Role: f.f.reader, Member: y, Grantor: root, Inherit: true}, {Role: x, Member: f.f.member, Grantor: root, Inherit: true}, {Role: y, Member: f.f.member, Grantor: root, Inherit: true}}
		want := roleRoutineGrantsWant("default_owner", "default_reader", "EXECUTE", "NO")
		for _, n := range []int{4, 3, 2} {
			next := cloneRoleFixture(roles)
			setFixtureEdges(next, edges[:n])
			f.f.e.RoleStore.PublishLocked(next)
			if n > 2 {
				f.check(t, f.state, f.f.member, want)
			} else {
				f.check(t, f.state, f.f.member)
			}
		}
	})
	t.Run("no_nonroutine_or_lookup_population", func(t *testing.T) {
		f := newFixture(t)
		relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20524}, ObjectNamespace: f.f.schema, ObjectName: "rrg_unit", ObjectRevision: 1, Owner: f.f.owner, RelationKind: catalog.TableRelation, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.ExecutePrivilege, 0)}}}}
		f.state.SetDescriptor(relation.Address(), relation)
		f.check(t, f.state, f.f.reader)
		in := f.input(t, f.state, f.f.e.Roles().LookupRef("postgres"))
		var absent catalog.RoutineIdentity
		for _, identity := range catalog.BuiltinRoutineIdentities() {
			if len(identity.Name) < 40 {
				if _, ok := in.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID}); !ok {
					absent = identity
					break
				}
			}
		}
		if absent.ID == 0 {
			t.Fatal("fixture lacks lookup-only control")
		}
		specific := fmt.Sprintf("%s_%d", absent.Name, absent.ID)
		for _, row := range roleRoutineGrantsRows(t, in) {
			if infoUserMappingsUnitText(t, row[4]) == specific {
				t.Fatal("lookup-only routine acquired grant rows", absent)
			}
		}
	})
}
