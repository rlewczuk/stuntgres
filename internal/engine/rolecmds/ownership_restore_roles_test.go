package rolecmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestRoleOwnershipDatabaseRestoreRoleGraph(t *testing.T) {
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	source := roleAttributeCreate(t, e, op, "source", false, parser.RoleOptions{})
	member := roleAttributeCreate(t, e, op, "member", false, parser.RoleOptions{})
	graph := cloneRoleFixture(e.RoleStore.Published)
	// Preserve a revoked seed edge and independent ordinary edge origins/options;
	// reconstructing the default graph would produce different authority.
	setFixtureEdges(graph, append(fixtureEdges(graph)[1:], authorization.Membership{Role: source.Ref, Member: member.Ref, Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Admin: true, Inherit: false, Set: true}))
	e.RoleStore.PublishLocked(graph)
	current := catalog.CloneState(e.Catalog.State)
	database := *current.MustDatabase(catalog.CurrentDatabaseAddress)
	database.Owner = source.Ref
	database.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: source.Ref, Grantee: authorization.ACLGrantee{Role: member.Ref}, Privileges: authorization.CreatePrivilege, GrantOptions: authorization.CreatePrivilege}, {Grantor: member.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.CreatePrivilege}}}
	current.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	e.Catalog.PublishLocked(current)
	edges := append([]authorization.Membership(nil), fixtureEdges(graph)...)
	entries := append([]authorization.ACLEntry(nil), database.ACL.Entries...)
	stage := e.RestoreStage()
	if stage.RoleStore.Published.Revision != graph.Revision || len(fixtureEdges(stage.RoleStore.Published)) != len(edges) {
		t.Fatal("restore reseeded or dropped membership graph")
	}
	for i, expected := range edges {
		expected.Role = stage.RoleStore.Domain.Principal(expected.Role.ID())
		expected.Member = stage.RoleStore.Domain.Principal(expected.Member.ID())
		expected.Grantor = stage.RoleStore.Domain.Principal(expected.Grantor.ID())
		if fixtureEdges(stage.RoleStore.Published)[i] != expected {
			t.Fatal("restore membership provenance/options", fixtureEdges(stage.RoleStore.Published)[i], expected)
		}
	}
	imported := stage.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
	if imported.Owner != (stage.RoleStore.Domain.Principal(source.Ref.ID())) || imported.ACL.Explicit != database.ACL.Explicit || len(imported.ACL.Entries) != len(entries) {
		t.Fatal("restore isolated database authority")
	}
	for i, expected := range entries {
		expected.Grantor = stage.RoleStore.Domain.Principal(expected.Grantor.ID())
		if !expected.Grantee.Public {
			expected.Grantee.Role = stage.RoleStore.Domain.Principal(expected.Grantee.Role.ID())
		}
		if imported.ACL.Entries[i] != expected {
			t.Fatal("restore CREATE ACL provenance", imported.ACL.Entries[i], expected)
		}
	}
	sourceRead, ok := stage.Roles().Lookup("source")
	if !ok || stage.ImportTargets[sourceRead.Ref] != source.Ref {
		t.Fatal("strict captured owner mapping")
	}
	if allowed, err := stage.Roles().CanSetRole(stage.RoleStore.Domain.Principal(member.Ref.ID()), sourceRead.Ref); err != nil || !allowed {
		t.Fatal("restore lost real SET authority", allowed, err)
	}
	if allowed, err := stage.Roles().InheritsRole(stage.RoleStore.Domain.Principal(member.Ref.ID()), sourceRead.Ref); err != nil || allowed {
		t.Fatal("restore invented INHERIT", allowed, err)
	}
	if !reflect.DeepEqual(edges, fixtureEdges(graph)) || !reflect.DeepEqual(entries, database.ACL.Entries) || e.Catalog.State != current {
		t.Fatal("restore changed source shared state")
	}
	changedEdges := fixtureEdges(stage.RoleStore.Published)
	changedEdges[0].Inherit = !changedEdges[0].Inherit
	stage.RoleStore.PublishLocked(cloneRoleFixture(stage.RoleStore.Published))
	setFixtureEdges(stage.RoleStore.Published, changedEdges)
	imported.ACL.Entries[0].Privileges = 0
	if !reflect.DeepEqual(edges, fixtureEdges(graph)) || !reflect.DeepEqual(entries, database.ACL.Entries) {
		t.Fatal("isolated restore aliases source membership/ACL arrays")
	}
}

func roleAttributeCreate(t *testing.T, e *engine.Engine, op *session.Operation, name string, login bool, o parser.RoleOptions) authorization.RoleDescriptor {
	t.Helper()
	roleAttributeApply(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: name}, DefaultLogin: login, Options: o})
	d, ok := e.Roles().Lookup(name)
	if !ok {
		t.Fatal("missing role", name)
	}
	return d
}

func roleAttributeApply(t *testing.T, e *engine.Engine, op *session.Operation, s parser.Statement) {
	t.Helper()
	r, err := e.Execute(op, s, 0, nil)
	if err != nil || r.Count != 0 || r.Rows != nil {
		t.Fatalf("attribute utility: result=%#v error=%v", r, err)
	}
}
