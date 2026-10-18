package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

// The role graph and epoch are identical in both reads. Only the canonical
// database descriptor changes, so neither a bootstrap shortcut nor a role
// revision cache can supply the expected authority.
func TestRoleOwnershipDatabaseImmutableRolePair(t *testing.T) {
	e := New()
	roles := cloneRoleFixture(e.roles)
	previous := roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	next := e.authorizationDomain.Principal(100001)
	member := e.authorizationDomain.Principal(100002)
	bootstrap := fixtureRole(roles, previous)
	bootstrap.Superuser = false
	putFixtureRole(roles, bootstrap)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: next, Name: "successor", Inherit: true})
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: member, Name: "member", Inherit: true})
	implicit := roles.root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	before := e.catalog.read(e.catalog.state)
	candidate := cloneCatalog(e.catalog.state)
	database := *candidate.MustDatabase(currentDatabaseAddress)
	database.Owner = next
	candidate.setDescriptor(currentDatabaseAddress, &database)
	after := e.catalog.read(candidate)
	oldRead, newRead := e.roleRead(before, roles), e.roleRead(after, roles)
	oldView, newView := catalogAuthorization(e, before), catalogAuthorization(e, after)
	oldView.roleRecord, oldView.roles = roles, oldRead
	newView.roleRecord, newView.roles = roles, newRead
	for _, item := range []struct {
		read         authorization.Read
		owner, other authorization.PrincipalRef
	}{{oldRead, previous, next}, {newRead, next, previous}} {
		for _, inherit := range []bool{false, true} {
			closure, err := item.read.RoleClosure(context.Background(), item.owner, inherit)
			if err != nil || !reflect.DeepEqual(closure, []authorization.PrincipalRef{item.owner, implicit}) {
				t.Fatal("canonical owner closure", closure, err)
			}
			closure, err = item.read.RoleClosure(context.Background(), item.other, inherit)
			if err != nil || !reflect.DeepEqual(closure, []authorization.PrincipalRef{item.other}) {
				t.Fatal("former owner leaked", closure, err)
			}
		}
		if ok, err := item.read.CanSetRole(item.owner, implicit); err != nil || !ok {
			t.Fatal("implicit SET", ok, err)
		}
		if ok, err := item.read.AdminOfRole(item.owner, implicit); err != nil || ok {
			t.Fatal("implicit ADMIN invented", ok, err)
		}
	}
	for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		graph := cloneRoleFixture(roles)
		setFixtureEdges(graph, []authorization.Membership{{Role: next, Member: member, Grantor: previous, Inherit: flags[0], Set: flags[1]}})
		read := e.roleRead(after, graph)
		closure, err := read.RoleClosure(context.Background(), member, false)
		if err != nil || !reflect.DeepEqual(closure, []authorization.PrincipalRef{member, next, implicit}) {
			t.Fatal("MEMBER owner hop", flags, closure, err)
		}
		if ok, err := read.InheritsRole(member, implicit); err != nil || ok != flags[0] {
			t.Fatal("INHERIT owner hop", flags, ok, err)
		}
		if ok, err := read.CanSetRole(member, implicit); err != nil || ok != flags[1] {
			t.Fatal("SET owner hop", flags, ok, err)
		}
		if ok, err := read.AdminOfRole(member, implicit); err != nil || ok {
			t.Fatal("member ADMIN invented", flags, ok, err)
		}
	}
	if before.Epoch() != after.Epoch() || oldView.Stamp().Roles != newView.Stamp().Roles || before.MustDatabase(currentDatabaseAddress).Owner != previous || len(fixtureEdges(roles)) != len(fixtureEdges(e.roles)) {
		t.Fatal("source pair mutated")
	}
}
func TestRoleOwnershipDatabaseRejectsMixedAuthorization(t *testing.T) {
	e := New()
	retained := e.CatalogRead()
	auth := catalogAuthorization(e, retained)
	candidate := cloneCatalog(retained.state)
	database := *candidate.MustDatabase(currentDatabaseAddress)
	database.ObjectRevision++
	candidate.setDescriptor(currentDatabaseAddress, &database)
	auth.catalog = e.catalog.read(candidate)
	ref := e.catalog.Domain.Ref(retained.Epoch(), currentDatabaseAddress)
	if _, err := auth.object(ref); err == nil {
		t.Fatal("mixed retained pair accepted")
	}
	auth.recaptureCatalog(auth.catalog)
	if _, err := auth.object(ref); err != nil {
		t.Fatal("matching retained pair rejected", err)
	}
	foreign := New()
	auth.roles = foreign.Roles()
	auth.pairedCatalog = foreign.CatalogRead()
	if _, err := auth.object(ref); err == nil {
		t.Fatal("foreign pair accepted")
	}
}

func TestRoleOwnershipDatabaseDiagnosticNouns(t *testing.T) {
	e, _, _, _, reader := ownershipUnitRoles()
	candidate := cloneCatalog(e.catalog.state)
	database := *candidate.MustDatabase(currentDatabaseAddress)
	database.ObjectName = `ownership "odd" database`
	candidate.setDescriptor(currentDatabaseAddress, &database)
	auth := catalogAuthorization(e, e.catalog.read(candidate))
	auth.state.CurrentUser = reader
	ref := e.catalog.Domain.Ref(candidate.epoch, currentDatabaseAddress)
	for _, tc := range []struct {
		requirement AccessRequirement
		message     string
	}{
		{AccessRequirement{Object: ref, OwnerOnly: true}, `must be owner of database ownership "odd" database`},
		{AccessRequirement{Object: ref, Privileges: authorization.CreatePrivilege}, `permission denied for database "ownership ""odd"" database"`},
	} {
		err := auth.Check(tc.requirement)
		expected := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: tc.message}
		if !reflect.DeepEqual(err, expected) {
			t.Fatal("distinct database diagnostic convention", err, "want", expected)
		}
	}
	if candidate.Get(currentDatabaseAddress) != &database || auth.pairedCatalog.state != candidate {
		t.Fatal("diagnostic check changed retained descriptor or role/catalog pair")
	}
}
