package core_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
)

func infoTriggersACLVisibility(t *testing.T) {
	t.Helper()
	e, scope, read, relation, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, function, "grant_unit", stored.TriggerUpdate)
	definition = infoTriggersWhen(t, e, scope, read, relation, definition)
	relation = triggerUnitInstall(t, e, relation, definition)
	roles := cloneRoleFixture(e.RoleStore.Published)
	reader := e.RoleStore.Domain.Principal(20001)
	member := e.RoleStore.Domain.Principal(20002)
	for _, actor := range []authorization.PrincipalRef{reader, member} {
		name := "trigger_reader"
		if actor == member {
			name = "trigger_member"
		}
		putFixtureRole(roles, authorization.RoleDescriptor{Ref: actor, Name: name, Login: true, Inherit: true, ConnectionLimit: -1})
	}
	setFixtureEdges(roles, append(fixtureEdges(roles), authorization.Membership{Role: relation.Owner, Member: member, Grantor: relation.Owner, Inherit: true, Set: true}))
	e.RoleStore.PublishLocked(roles)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTriggersOID)
	check := func(table *catalog.Relation, actor authorization.PrincipalRef, want int, condition bool) {
		t.Helper()
		state := catalog.CloneState(e.Catalog.State)
		state.SetDescriptor(table.Address(), table)
		in := infoTriggersInput(t, e, scope)
		view := e.Catalog.Read(state)
		runtime := in.Runtime.(core.EngineCatalogRuntime)
		runtime.Auth.State.CurrentUser = actor
		runtime.Auth.Recapture(view)
		in.Catalog, in.Roles, in.Runtime = view, runtime.Auth.Roles, runtime
		rows, err := produce(in)
		if err != nil || len(rows) != want {
			t.Fatal("ACL visibility", actor, table.Security, rows, err)
		}
		if want != 0 && rows[0][8].IsNull() == condition {
			t.Fatal("owner-only condition", rows)
		}
	}
	for _, explicit := range []bool{false, true} {
		copy := *relation
		copy.Security.ACL = authorization.ACLValue{Explicit: explicit}
		check(&copy, reader, 0, false)
		check(&copy, relation.Owner, 1, true)
		check(&copy, member, 1, true)
	}
	for _, privilege := range []authorization.Privileges{authorization.SelectPrivilege, authorization.MaintainPrivilege, authorization.InsertPrivilege, authorization.UpdatePrivilege, authorization.DeletePrivilege, authorization.TruncatePrivilege, authorization.ReferencesPrivilege, authorization.TriggerPrivilege} {
		copy := *relation
		copy.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: relation.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: privilege}}}
		want := 1
		if privilege == authorization.SelectPrivilege || privilege == authorization.MaintainPrivilege {
			want = 0
		}
		check(&copy, reader, want, false)
	}
	for _, privilege := range []authorization.Privileges{authorization.SelectPrivilege, authorization.InsertPrivilege, authorization.UpdatePrivilege, authorization.ReferencesPrivilege} {
		copy := *relation
		copy.Security.ACL = authorization.ACLValue{Explicit: true}
		copy.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{{Grantor: relation.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: privilege}}}}
		want := 1
		if privilege == authorization.SelectPrivilege {
			want = 0
		}
		check(&copy, reader, want, false)
		copy.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{1: {}}
		check(&copy, reader, 0, false)
	}
	old := infoTriggersInput(t, e, scope)
	oldRuntime := old.Runtime.(core.EngineCatalogRuntime)
	oldRuntime.Auth.State.CurrentUser = member
	old.Runtime = oldRuntime
	retained, err := produce(old)
	if err != nil || len(retained) != 1 {
		t.Fatal("old role capture", retained, err)
	}
	oldRoles := oldRuntime.Auth.RoleRecord
	successor := cloneRoleFixture(e.RoleStore.Published)
	setFixtureEdges(successor, nil)
	e.RoleStore.PublishLocked(successor)
	if oldRoles == e.RoleStore.Published {
		t.Fatal("role root did not advance")
	}
	check(relation, member, 0, false)
	again, err := produce(old)
	if err != nil || len(again) != 1 || retained[0][8].IsNull() {
		t.Fatal("retained paired authority changed", again, err)
	}
	mixed := old
	mixed.Roles = infoTriggersInput(t, e, scope).Roles
	if rows, err := produce(mixed); rows != nil || err == nil {
		t.Fatal("mixed role frontier accepted", rows, err)
	}
}
