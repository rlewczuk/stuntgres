package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/access"
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
	previous := roles.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	next := e.roles.domain.Principal(100001)
	member := e.roles.domain.Principal(100002)
	bootstrap := fixtureRole(roles, previous)
	bootstrap.Superuser = false
	putFixtureRole(roles, bootstrap)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: next, Name: "successor", Inherit: true})
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: member, Name: "member", Inherit: true})
	implicit := roles.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	published, revision, root := e.roles.published, roles.Revision, roles.Root
	memberships := append([]authorization.Membership(nil), fixtureEdges(roles)...)
	before := e.catalog.Current()
	candidate := catalog.CloneState(e.catalog.State)
	database := *candidate.MustDatabase(catalog.CurrentDatabaseAddress)
	database.Owner = next
	candidate.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	after := e.catalog.Read(candidate)
	oldRead, newRead := e.roles.read(&e.catalog.Owner, before, roles), e.roles.read(&e.catalog.Owner, after, roles)
	oldView, newView := catalogAuthorization(e, before), catalogAuthorization(e, after)
	oldView.RoleRecord, oldView.Roles = roles, oldRead
	newView.RoleRecord, newView.Roles = roles, newRead
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
		exactEdges := append([]authorization.Membership(nil), fixtureEdges(graph)...)
		read := e.roles.read(&e.catalog.Owner, after, graph)
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
		if !reflect.DeepEqual(fixtureEdges(graph), exactEdges) {
			t.Fatal("implicit owner hop mutated explicit membership identities/flags")
		}
		for _, edge := range fixtureEdges(graph) {
			if edge.Role == implicit {
				t.Fatal("synthetic database-owner membership", edge)
			}
		}
	}
	if before.Epoch() != after.Epoch() || oldView.Stamp().Roles != newView.Stamp().Roles || before.MustDatabase(catalog.CurrentDatabaseAddress).Owner != previous || e.roles.published != published || roles.Revision != revision || !reflect.DeepEqual(roles.Root, root) || !reflect.DeepEqual(fixtureEdges(roles), memberships) {
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
	auth.Catalog = e.catalog.Read(candidate)
	ref := e.catalog.Domain.Ref(retained.Epoch(), catalog.CurrentDatabaseAddress)
	if _, err := auth.Object(ref); err == nil {
		t.Fatal("mixed retained pair accepted")
	}
	auth.Recapture(auth.Catalog)
	if _, err := auth.Object(ref); err != nil {
		t.Fatal("matching retained pair rejected", err)
	}
	foreign := New()
	auth.Roles = foreign.Roles()
	auth.PairedCatalog = foreign.CatalogRead()
	if _, err := auth.Object(ref); err == nil {
		t.Fatal("foreign pair accepted")
	}
}

func TestRoleOwnershipDatabaseDiagnosticNouns(t *testing.T) {
	e, _, _, _, reader := ownershipUnitRoles()
	candidate := catalog.CloneState(e.catalog.State)
	database := *candidate.MustDatabase(catalog.CurrentDatabaseAddress)
	database.ObjectName = `ownership "odd" database`
	candidate.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	auth := catalogAuthorization(e, e.catalog.Read(candidate))
	auth.State.CurrentUser = reader
	ref := e.catalog.Domain.Ref(candidate.Epoch(), catalog.CurrentDatabaseAddress)
	for _, tc := range []struct {
		requirement access.Requirement
		message     string
	}{
		{access.Requirement{Object: ref, OwnerOnly: true}, `must be owner of database ownership "odd" database`},
		{access.Requirement{Object: ref, Privileges: authorization.CreatePrivilege}, `permission denied for database "ownership ""odd"" database"`},
	} {
		err := auth.Check(tc.requirement)
		expected := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: tc.message}
		if !reflect.DeepEqual(err, expected) {
			t.Fatal("distinct database diagnostic convention", err, "want", expected)
		}
	}
	if candidate.Get(catalog.CurrentDatabaseAddress) != &database || auth.PairedCatalog.State() != candidate {
		t.Fatal("diagnostic check changed retained descriptor or role/catalog pair")
	}
}
