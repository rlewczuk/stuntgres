package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/parser"
)

func TestRoleOwnershipDatabaseRestoreRoleGraph(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	source := roleAttributeCreate(t, e, op, "source", false, parser.RoleOptions{})
	member := roleAttributeCreate(t, e, op, "member", false, parser.RoleOptions{})
	graph := cloneRoles(e.roles)
	// Preserve a revoked seed edge and independent ordinary edge origins/options;
	// reconstructing the default graph would produce different authority.
	graph.edges = append(graph.edges[1:], Membership{Role: source.Ref, Member: member.Ref, Grantor: e.roles.names["postgres"], Admin: true, Inherit: false, Set: true})
	e.roles = graph
	current := cloneCatalog(e.state)
	database := *current.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	database.owner = source.Ref
	database.acl = ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: source.Ref, Grantee: ACLGrantee{Role: member.Ref}, Privileges: CreatePrivilege, GrantOptions: CreatePrivilege}, {Grantor: member.Ref, Grantee: ACLGrantee{Public: true}, Privileges: CreatePrivilege}}}
	current.descriptors[currentDatabaseAddress] = &database
	e.state = current
	edges := append([]Membership(nil), graph.edges...)
	entries := append([]ACLEntry(nil), database.acl.Entries...)
	stage := e.restoreStage()
	if stage.roles.revision != graph.revision || len(stage.roles.edges) != len(edges) {
		t.Fatal("restore reseeded or dropped membership graph")
	}
	for i, expected := range edges {
		expected.Role.owner = stage
		expected.Member.owner = stage
		expected.Grantor.owner = stage
		if stage.roles.edges[i] != expected {
			t.Fatal("restore membership provenance/options", stage.roles.edges[i], expected)
		}
	}
	imported := stage.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	if imported.owner != (PrincipalRef{owner: stage, id: source.Ref.id}) || imported.acl.Explicit != database.acl.Explicit || len(imported.acl.Entries) != len(entries) {
		t.Fatal("restore isolated database authority")
	}
	for i, expected := range entries {
		expected.Grantor.owner = stage
		if !expected.Grantee.Public {
			expected.Grantee.Role.owner = stage
		}
		if imported.acl.Entries[i] != expected {
			t.Fatal("restore CREATE ACL provenance", imported.acl.Entries[i], expected)
		}
	}
	sourceRead, ok := stage.Roles().Lookup("source")
	if !ok || stage.importTargets[sourceRead.Ref] != source.Ref {
		t.Fatal("strict captured owner mapping")
	}
	if allowed, err := stage.Roles().canSetRole(PrincipalRef{owner: stage, id: member.Ref.id}, sourceRead.Ref); err != nil || !allowed {
		t.Fatal("restore lost real SET authority", allowed, err)
	}
	if allowed, err := stage.Roles().inheritsRole(PrincipalRef{owner: stage, id: member.Ref.id}, sourceRead.Ref); err != nil || allowed {
		t.Fatal("restore invented INHERIT", allowed, err)
	}
	if !reflect.DeepEqual(edges, graph.edges) || !reflect.DeepEqual(entries, database.acl.Entries) || e.state != current {
		t.Fatal("restore changed source shared state")
	}
	stage.roles.edges[0].Inherit = !stage.roles.edges[0].Inherit
	imported.acl.Entries[0].Privileges = 0
	if !reflect.DeepEqual(edges, graph.edges) || !reflect.DeepEqual(entries, database.acl.Entries) {
		t.Fatal("isolated restore aliases source membership/ACL arrays")
	}
}
