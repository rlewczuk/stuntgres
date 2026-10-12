package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

// The role graph and epoch are identical in both reads. Only the canonical
// database descriptor changes, so neither a bootstrap shortcut nor a role
// revision cache can supply the expected authority.
func TestRoleOwnershipDatabaseImmutableRolePair(t *testing.T) {
	e := New()
	roles := cloneRoles(e.roles)
	previous := roles.names["postgres"]
	next := PrincipalRef{owner: e, id: 100001}
	member := PrincipalRef{owner: e, id: 100002}
	bootstrap := roles.roles[previous]
	bootstrap.Superuser = false
	roles.roles[previous] = bootstrap
	roles.roles[next] = RoleDescriptor{Ref: next, Name: "successor", Inherit: true}
	roles.roles[member] = RoleDescriptor{Ref: member, Name: "member", Inherit: true}
	implicit := roles.names["pg_database_owner"]
	before := CatalogRead{e, e.state}
	candidate := cloneCatalog(e.state)
	database := *candidate.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	database.owner = next
	candidate.descriptors[currentDatabaseAddress] = &database
	after := CatalogRead{e, candidate}
	oldRead, newRead := RoleRead{e, roles, before}, RoleRead{e, roles, after}
	for _, item := range []struct {
		read         RoleRead
		owner, other PrincipalRef
	}{{oldRead, previous, next}, {newRead, next, previous}} {
		for _, inherit := range []bool{false, true} {
			closure, err := item.read.roleClosure(context.Background(), item.owner, inherit)
			if err != nil || !reflect.DeepEqual(closure, []PrincipalRef{item.owner, implicit}) {
				t.Fatal("canonical owner closure", closure, err)
			}
			closure, err = item.read.roleClosure(context.Background(), item.other, inherit)
			if err != nil || !reflect.DeepEqual(closure, []PrincipalRef{item.other}) {
				t.Fatal("former owner leaked", closure, err)
			}
		}
		if ok, err := item.read.canSetRole(item.owner, implicit); err != nil || !ok {
			t.Fatal("implicit SET", ok, err)
		}
		if ok, err := item.read.adminOfRole(item.owner, implicit); err != nil || ok {
			t.Fatal("implicit ADMIN invented", ok, err)
		}
	}
	for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		graph := cloneRoles(roles)
		graph.edges = []Membership{{Role: next, Member: member, Grantor: previous, Inherit: flags[0], Set: flags[1]}}
		read := RoleRead{e, graph, after}
		closure, err := read.roleClosure(context.Background(), member, false)
		if err != nil || !reflect.DeepEqual(closure, []PrincipalRef{member, next, implicit}) {
			t.Fatal("MEMBER owner hop", flags, closure, err)
		}
		if ok, err := read.inheritsRole(member, implicit); err != nil || ok != flags[0] {
			t.Fatal("INHERIT owner hop", flags, ok, err)
		}
		if ok, err := read.canSetRole(member, implicit); err != nil || ok != flags[1] {
			t.Fatal("SET owner hop", flags, ok, err)
		}
		if ok, err := read.adminOfRole(member, implicit); err != nil || ok {
			t.Fatal("member ADMIN invented", flags, ok, err)
		}
	}
	if before.Epoch() != after.Epoch() || oldRead.state.revision != newRead.state.revision || before.state.descriptors[currentDatabaseAddress].(*databaseDescriptor).owner != previous || len(roles.edges) != len(e.roles.edges) {
		t.Fatal("source pair mutated")
	}
}
func TestRoleOwnershipDatabaseRejectsMixedAuthorization(t *testing.T) {
	e := New()
	retained := e.CatalogRead()
	auth := catalogAuthorization(e, retained)
	candidate := cloneCatalog(retained.state)
	database := *candidate.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	database.revision++
	candidate.descriptors[currentDatabaseAddress] = &database
	auth.catalog = CatalogRead{e, candidate}
	ref := ObjectRef{owner: e.owner, Epoch: retained.Epoch(), Address: currentDatabaseAddress}
	if _, err := auth.object(ref); err == nil {
		t.Fatal("mixed retained pair accepted")
	}
	auth.roles.catalog = auth.catalog
	if _, err := auth.object(ref); err != nil {
		t.Fatal("matching retained pair rejected", err)
	}
	foreign := New()
	auth.roles.catalog = foreign.CatalogRead()
	if _, err := auth.object(ref); err == nil {
		t.Fatal("foreign pair accepted")
	}
}

func TestRoleOwnershipDatabaseDiagnosticNouns(t *testing.T) {
	e, _, _, _, reader := ownershipUnitRoles()
	candidate := cloneCatalog(e.state)
	database := *candidate.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	database.name = `ownership "odd" database`
	candidate.descriptors[currentDatabaseAddress] = &database
	auth := catalogAuthorization(e, CatalogRead{e, candidate})
	auth.state.CurrentUser = reader
	ref := ObjectRef{owner: e.owner, Epoch: candidate.epoch, Address: currentDatabaseAddress}
	for _, tc := range []struct {
		requirement AccessRequirement
		message     string
	}{
		{AccessRequirement{Object: ref, OwnerOnly: true}, `must be owner of database ownership "odd" database`},
		{AccessRequirement{Object: ref, Privileges: CreatePrivilege}, `permission denied for database "ownership ""odd"" database"`},
	} {
		err := auth.Check(tc.requirement)
		expected := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: tc.message}
		if !reflect.DeepEqual(err, expected) {
			t.Fatal("distinct database diagnostic convention", err, "want", expected)
		}
	}
	if candidate.descriptors[currentDatabaseAddress] != &database || auth.roles.catalog.state != candidate {
		t.Fatal("diagnostic check changed retained descriptor or role/catalog pair")
	}
}
