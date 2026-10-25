package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
)

func TestInfoRoutinePrivilegesOrigins(t *testing.T) {
	testRoutinePrivilegesOrigins(t, newRoutinePrivilegesFixture, true)
	t.Run("public_does_not_reveal_named_origins", func(t *testing.T) {
		f := newRoutinePrivilegesFixture(t)
		routine := *f.routine
		routine.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
			{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Role: f.f.owner}, Privileges: authorization.ExecutePrivilege},
		}}
		f.state.SetDescriptor(routine.Address(), &routine)
		public := roleRoutineGrantsWant("default_owner", "PUBLIC", "EXECUTE", "NO")
		owner := roleRoutineGrantsWant("default_owner", "default_owner", "EXECUTE", "YES")
		f.check(t, f.state, f.f.reader, public)
		f.check(t, f.state, f.f.owner, public, owner)

		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		power := fixtureRole(roles, f.f.reader)
		power.Superuser = true
		putFixtureRole(roles, power)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.reader, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
		f.f.e.RoleStore.PublishLocked(roles)
		f.check(t, f.state, f.f.member, public)
		f.check(t, f.state, f.f.reader, public, owner)
	})
}
