package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
	"testing"
)

// The role graph and epoch are identical in both reads. Only the canonical
// database descriptor changes, so neither a bootstrap shortcut nor a role
// revision cache can supply the expected authority.
func TestRoleOwnershipDatabaseImmutableRolePair(t *testing.T) {
	e := New()
	roles := cloneRoleFixture(e.roles.published)
	previous := roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	next := e.roles.domain.Principal(100001)
	member := e.roles.domain.Principal(100002)
	bootstrap := fixtureRole(roles, previous)
	bootstrap.Superuser = false
	putFixtureRole(roles, bootstrap)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: next, Name: "successor", Inherit: true})
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: member, Name: "member", Inherit: true})
	implicit := roles.root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	before := e.catalog.read(e.catalog.state)
	candidate := catalog.CloneState(e.catalog.state)
	database := *candidate.MustDatabase(catalog.CurrentDatabaseAddress)
	database.Owner = next
	candidate.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	after := e.catalog.read(candidate)
	oldRead, newRead := e.roles.read(e.catalog.owner(), before, roles), e.roles.read(e.catalog.owner(), after, roles)
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
		read := e.roles.read(e.catalog.owner(), after, graph)
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
	if before.Epoch() != after.Epoch() || oldView.Stamp().Roles != newView.Stamp().Roles || before.MustDatabase(catalog.CurrentDatabaseAddress).Owner != previous || len(fixtureEdges(roles)) != len(fixtureEdges(e.roles.published)) {
		t.Fatal("source pair mutated")
	}
}
func TestRoleOwnershipDatabaseRejectsMixedAuthorization(t *testing.T) {
	e := New()
	retained := e.CatalogRead()
	auth := catalogAuthorization(e, retained)
	candidate := catalog.CloneState(retained.State())
	database := *candidate.MustDatabase(catalog.CurrentDatabaseAddress)
	database.ObjectRevision++
	candidate.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	auth.catalog = e.catalog.read(candidate)
	ref := e.catalog.Domain.Ref(retained.Epoch(), catalog.CurrentDatabaseAddress)
	if _, err := auth.object(ref); err == nil {
		t.Fatal("mixed retained pair accepted")
	}
	auth.Recapture(auth.catalog)
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
	candidate := catalog.CloneState(e.catalog.state)
	database := *candidate.MustDatabase(catalog.CurrentDatabaseAddress)
	database.ObjectName = `ownership "odd" database`
	candidate.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	auth := catalogAuthorization(e, e.catalog.read(candidate))
	auth.state.CurrentUser = reader
	ref := e.catalog.Domain.Ref(candidate.Epoch(), catalog.CurrentDatabaseAddress)
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
	if candidate.Get(catalog.CurrentDatabaseAddress) != &database || auth.pairedCatalog.State() != candidate {
		t.Fatal("diagnostic check changed retained descriptor or role/catalog pair")
	}
}
