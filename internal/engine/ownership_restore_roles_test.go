package engine

import (
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"testing"
)

func TestRoleOwnershipDatabaseRestoreRoleGraph(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	source := roleAttributeCreate(t, e, op, "source", false, parser.RoleOptions{})
	member := roleAttributeCreate(t, e, op, "member", false, parser.RoleOptions{})
	graph := cloneRoleFixture(e.roles)
	// Preserve a revoked seed edge and independent ordinary edge origins/options;
	// reconstructing the default graph would produce different authority.
	setFixtureEdges(graph, append(fixtureEdges(graph)[1:], authorization.Membership{Role: source.Ref, Member: member.Ref, Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Admin: true, Inherit: false, Set: true}))
	e.roles = graph
	current := catalog.CloneState(e.catalog.state)
	database := *current.MustDatabase(catalog.CurrentDatabaseAddress)
	database.Owner = source.Ref
	database.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: source.Ref, Grantee: authorization.ACLGrantee{Role: member.Ref}, Privileges: authorization.CreatePrivilege, GrantOptions: authorization.CreatePrivilege}, {Grantor: member.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.CreatePrivilege}}}
	current.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	e.catalog.state = current
	edges := append([]authorization.Membership(nil), fixtureEdges(graph)...)
	entries := append([]authorization.ACLEntry(nil), database.ACL.Entries...)
	stage := e.restoreStage()
	if stage.roles.revision != graph.revision || len(fixtureEdges(stage.roles)) != len(edges) {
		t.Fatal("restore reseeded or dropped membership graph")
	}
	for i, expected := range edges {
		expected.Role = stage.authorizationDomain.Principal(expected.Role.ID())
		expected.Member = stage.authorizationDomain.Principal(expected.Member.ID())
		expected.Grantor = stage.authorizationDomain.Principal(expected.Grantor.ID())
		if fixtureEdges(stage.roles)[i] != expected {
			t.Fatal("restore membership provenance/options", fixtureEdges(stage.roles)[i], expected)
		}
	}
	imported := stage.catalog.state.MustDatabase(catalog.CurrentDatabaseAddress)
	if imported.Owner != (stage.authorizationDomain.Principal(source.Ref.ID())) || imported.ACL.Explicit != database.ACL.Explicit || len(imported.ACL.Entries) != len(entries) {
		t.Fatal("restore isolated database authority")
	}
	for i, expected := range entries {
		expected.Grantor = stage.authorizationDomain.Principal(expected.Grantor.ID())
		if !expected.Grantee.Public {
			expected.Grantee.Role = stage.authorizationDomain.Principal(expected.Grantee.Role.ID())
		}
		if imported.ACL.Entries[i] != expected {
			t.Fatal("restore CREATE ACL provenance", imported.ACL.Entries[i], expected)
		}
	}
	sourceRead, ok := stage.Roles().Lookup("source")
	if !ok || stage.importTargets[sourceRead.Ref] != source.Ref {
		t.Fatal("strict captured owner mapping")
	}
	if allowed, err := stage.Roles().CanSetRole(stage.authorizationDomain.Principal(member.Ref.ID()), sourceRead.Ref); err != nil || !allowed {
		t.Fatal("restore lost real SET authority", allowed, err)
	}
	if allowed, err := stage.Roles().InheritsRole(stage.authorizationDomain.Principal(member.Ref.ID()), sourceRead.Ref); err != nil || allowed {
		t.Fatal("restore invented INHERIT", allowed, err)
	}
	if !reflect.DeepEqual(edges, fixtureEdges(graph)) || !reflect.DeepEqual(entries, database.ACL.Entries) || e.catalog.state != current {
		t.Fatal("restore changed source shared state")
	}
	changedEdges := fixtureEdges(stage.roles)
	changedEdges[0].Inherit = !changedEdges[0].Inherit
	stage.roles = cloneRoleFixture(stage.roles)
	setFixtureEdges(stage.roles, changedEdges)
	imported.ACL.Entries[0].Privileges = 0
	if !reflect.DeepEqual(edges, fixtureEdges(graph)) || !reflect.DeepEqual(entries, database.ACL.Entries) {
		t.Fatal("isolated restore aliases source membership/ACL arrays")
	}
}
