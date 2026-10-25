package engine

import (
	"fmt"
	"testing"

	"stuntgres/internal/engine/authorization"
)

func TestInfoRoleUsageGrantsOrigins(t *testing.T) {
	t.Run("defaults_empty_masks", func(t *testing.T) {
		f := newRoleUsageGrantsFixture(t)
		f.check(t, f.state, f.f.owner)
		udt := *f.udt
		udt.ACL = authorization.ACLValue{}
		f.state.SetDescriptor(udt.Address(), &udt)
		owner := roleUsageGrantsWant("default_owner", "default_owner", "USAGE", "YES")
		public := roleUsageGrantsWant("default_owner", "PUBLIC", "USAGE", "NO")
		f.check(t, f.state, f.f.owner, owner, public)
		f.check(t, f.state, f.f.member, owner, public)
		f.check(t, f.state, f.f.reader)
		f.check(t, f.state, f.f.e.Roles().LookupRef("postgres"), owner, public)
		for _, mask := range []authorization.Privileges{authorization.SelectPrivilege, authorization.UpdatePrivilege, authorization.ExecutePrivilege, authorization.UsagePrivilege, authorization.UsagePrivilege | authorization.SelectPrivilege} {
			udt.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, mask, authorization.UsagePrivilege)}}
			f.state.SetDescriptor(udt.Address(), &udt)
			var want [][8]string
			if mask&authorization.UsagePrivilege != 0 {
				want = append(want, roleUsageGrantsWant("default_owner", "default_reader", "USAGE", "YES"))
			}
			f.check(t, f.state, f.f.reader, want...)
		}
		udt.ACL = authorization.ACLValue{Explicit: true}
		f.state.SetDescriptor(udt.Address(), &udt)
		f.check(t, f.state, f.f.owner)
		f.check(t, f.state, f.f.e.Roles().LookupRef("postgres"))
	})
	t.Run("separate_origins_grantee_ownership", func(t *testing.T) {
		f := newRoleUsageGrantsFixture(t)
		udt := *f.udt
		first := defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)
		second := first
		second.Grantor = f.f.member
		udt.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{first, second}}
		f.state.SetDescriptor(udt.Address(), &udt)
		a := roleUsageGrantsWant("default_owner", "default_reader", "USAGE", "NO")
		b := roleUsageGrantsWant("default_member", "default_reader", "USAGE", "NO")
		f.check(t, f.state, f.f.reader, a, b)
		f.check(t, f.state, f.f.owner, a)
		f.check(t, f.state, f.f.member, a, b)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.owner, Member: f.f.reader, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
		f.f.e.RoleStore.PublishLocked(roles)
		a[7], b[7] = "YES", "YES"
		f.check(t, f.state, f.f.reader, a, b)
		setOnly := cloneRoleFixture(roles)
		setFixtureEdges(setOnly, []authorization.Membership{{Role: f.f.owner, Member: f.f.reader, Grantor: f.f.e.Roles().LookupRef("postgres"), Set: true, Admin: true}})
		f.f.e.RoleStore.PublishLocked(setOnly)
		a[7], b[7] = "NO", "NO"
		f.check(t, f.state, f.f.reader, a, b)
		power := cloneRoleFixture(setOnly)
		reader := fixtureRole(power, f.f.reader)
		reader.Superuser = true
		putFixtureRole(power, reader)
		f.f.e.RoleStore.PublishLocked(power)
		a[7], b[7] = "YES", "YES"
		f.check(t, f.state, f.f.reader, a, b)
	})
	t.Run("public_actual_superuser", func(t *testing.T) {
		f := newRoleUsageGrantsFixture(t)
		udt := *f.udt
		udt.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}
		f.state.SetDescriptor(udt.Address(), &udt)
		public := roleUsageGrantsWant("default_owner", "PUBLIC", "USAGE", "NO")
		f.check(t, f.state, f.f.reader)
		f.check(t, f.state, f.f.owner, public)
		f.check(t, f.state, f.f.member, public)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		power := fixtureRole(roles, f.f.reader)
		power.Superuser = true
		putFixtureRole(roles, power)
		setFixtureEdges(roles, []authorization.Membership{{Role: f.f.reader, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
		f.f.e.RoleStore.PublishLocked(roles)
		f.check(t, f.state, f.f.member)
		f.check(t, f.state, f.f.reader, public)
	})
	t.Run("recursive_multiple_paths", func(t *testing.T) {
		f := newRoleUsageGrantsFixture(t)
		udt := *f.udt
		udt.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)}}
		f.state.SetDescriptor(udt.Address(), &udt)
		roles := cloneRoleFixture(f.f.e.RoleStore.Published)
		root := f.f.e.Roles().LookupRef("postgres")
		x, y := f.f.e.RoleStore.Domain.Principal(20531), f.f.e.RoleStore.Domain.Principal(20532)
		for i, ref := range []authorization.PrincipalRef{x, y} {
			putFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: fmt.Sprint("ug_path", i), Inherit: true, ConnectionLimit: -1})
		}
		edges := []authorization.Membership{{Role: f.f.reader, Member: x, Grantor: root, Inherit: true}, {Role: f.f.reader, Member: y, Grantor: root, Inherit: true}, {Role: x, Member: f.f.member, Grantor: root, Inherit: true}, {Role: y, Member: f.f.member, Grantor: root, Inherit: true}}
		want := roleUsageGrantsWant("default_owner", "default_reader", "USAGE", "NO")
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
}
