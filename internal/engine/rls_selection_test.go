package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/view"
	"testing"
)

func TestRLSSelection(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	for _, q := range []string{"CREATE ROLE selection_actor", "CREATE ROLE selection_group", "CREATE ROLE selection_other", "GRANT selection_group TO selection_actor WITH INHERIT TRUE"} {
		roleExecute(t, e, op, q)
	}
	roles := e.roles.published.Root.Read(authorization.PrincipalRef{})
	actor, group, other := roles.LookupRef("selection_actor"), roles.LookupRef("selection_group"), roles.LookupRef("selection_other")
	using, err := policyUnitBind(t, e, op, d, "FALSE")
	if err != nil {
		t.Fatal(err)
	}
	check, err := policyUnitBind(t, e, op, d, "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	next := *d
	next.Security.RLSEnabled = true
	next.Security.Policies = []catalog.PolicyDefinition{
		{Name: "all", Permissive: true, Command: catalog.PolicyAll, Roles: []catalog.PolicyRole{{Ref: other}, {Ref: group}}, Using: view.PolicyPayloadOf(using), Check: view.PolicyPayloadOf(check)},
		{Name: "select_restriction", Command: catalog.PolicySelect, Roles: []catalog.PolicyRole{{Public: true}}, Using: view.PolicyPayloadOf(check)},
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	selection, err := access.SelectRowSecurity(auth.Roles, &next, actor, catalog.PolicyUpdate, true, true)
	if err != nil || selection.Count != 2 || selection.Denied() {
		t.Fatal(selection, err)
	}
	if len(selection.Groups[0].Permissive) != 1 || selection.Groups[0].Permissive[0].Payload != view.PolicyPayloadOf(check) || len(selection.Groups[0].Restrictive) != 0 {
		t.Fatal("write command did not select CHECK", selection)
	}
	if len(selection.Groups[1].Permissive) != 1 || selection.Groups[1].Permissive[0].Payload != view.PolicyPayloadOf(using) || len(selection.Groups[1].Restrictive) != 1 || selection.Groups[1].Restrictive[0].Name != "select_restriction" {
		t.Fatal("SELECT NEW did not select USING", selection)
	}
	next.Security.Policies[0].Using = nil
	next.Security.Policies[0].Check = nil
	if selection.Groups[0].Permissive[0].Payload != view.PolicyPayloadOf(check) || selection.Groups[1].Permissive[0].Payload != view.PolicyPayloadOf(using) {
		t.Fatal("selection aliases mutable policy slots")
	}
	denied, err := access.SelectRowSecurity(auth.Roles, &next, actor, catalog.PolicySelect, false, false)
	if err != nil || !denied.Denied() || len(denied.Groups[0].Restrictive) != 1 {
		t.Fatal("missing permissive grant", denied, err)
	}
}
