package core_test

import (
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
)

func TestRLSBaseDecision(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	// Incidental internal catalog setup; assertions target the private metadata
	// decision function, never public SQL results.
	for _, q := range []string{"CREATE ROLE rls_unit_owner", "CREATE ROLE rls_unit_reader", "CREATE ROLE rls_unit_bypass BYPASSRLS", "CREATE ROLE rls_unit_member", "GRANT rls_unit_bypass TO rls_unit_member", "GRANT rls_unit_owner TO rls_unit_reader WITH INHERIT TRUE"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	owner, reader, bypass, member := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_unit_owner"), e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_unit_reader"), e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_unit_bypass"), e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_unit_member")
	using, err := policyUnitBind(t, e, op, d, "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	next := *d
	next.Owner = owner
	check := func(label string, p authorization.PrincipalRef, command catalog.PolicyCommand, proposed bool, count int, denied bool) {
		t.Helper()
		got, err := access.SelectRowSecurity(auth.Roles, &next, p, command, proposed, false)
		if err != nil || got.Count != count || got.Denied() != denied {
			t.Fatalf("%s got %#v,%v want count=%d denied=%v", label, got, err, count, denied)
		}
	}
	check("disabled", member, catalog.PolicySelect, false, 0, false)
	next.Security.RLSEnabled = true
	check("owner", owner, catalog.PolicySelect, false, 0, false)
	check("inherited owner", reader, catalog.PolicySelect, false, 0, false)
	next.Security.RLSForced = true
	check("forced owner", owner, catalog.PolicySelect, false, 1, true)
	check("direct bypass", bypass, catalog.PolicySelect, false, 0, false)
	check("attribute not inherited", member, catalog.PolicySelect, false, 1, true)
	check("direct superuser", d.Owner, catalog.PolicySelect, false, 0, false)
	next.Security.Policies = []catalog.PolicyDefinition{{Permissive: false, Command: catalog.PolicyAll, Roles: []catalog.PolicyRole{{Public: true}}, Using: view.PolicyPayloadOf(using)}}
	check("restrictive only", member, catalog.PolicySelect, false, 1, true)
	next.Security.Policies[0].Permissive = true
	next.Security.Policies[0].Roles = []catalog.PolicyRole{{Ref: owner}}
	check("role inapplicable", member, catalog.PolicySelect, false, 1, true)
	check("inherited policy", reader, catalog.PolicySelect, false, 1, false)
	next.Security.Policies[0].Roles = []catalog.PolicyRole{{Public: true}}
	check("public expression retained", member, catalog.PolicySelect, false, 1, false)
	check("ALL check falls back", member, catalog.PolicyInsert, true, 1, false)
	next.Security.Policies[0].Command = catalog.PolicySelect
	check("command inapplicable", member, catalog.PolicyInsert, true, 1, true)
	got, err := access.SelectRowSecurity(auth.Roles, &next, member, catalog.PolicyUpdate, false, true)
	if err != nil || got.Count != 2 || !got.Denied() {
		t.Fatal("same-phase false conjunction", got, err)
	}
	next.Security.Policies[0].Using = nil
	check("omitted using", member, catalog.PolicySelect, false, 1, true)
	next.Security.Policies[0].Command = catalog.PolicyInsert
	next.Security.Policies[0].Check = view.PolicyPayloadOf(using)
	check("explicit check", member, catalog.PolicyInsert, true, 1, false)
	got, err = access.SelectRowSecurity(auth.Roles, &next, member, catalog.PolicyInsert, true, true)
	if err != nil || got.Count != 2 || !got.Denied() {
		t.Fatal("proposed SELECT conjunction", got, err)
	}
	t.Run("owned_empty_source", func(t *testing.T) {
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := scope.Finish(nil); err != nil {
				t.Error(err)
			}
		}()
		ast := enginetest.Statement(t, "SELECT id FROM t")
		if _, err := scope.Describe(ast); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		b, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		ref := b.QuerySources[0].Ref
		dataView, err := scope.AcquireDataViewLocked(e, ref)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		source, err := view.CaptureDeniedQuerySource(dataView, ref)
		if err != nil {
			t.Fatal(err)
		}
		core.BorrowQueryAuthority(source, scope)
		defer source.Close()
		if source.View == nil || !source.RlsDenied || len(source.Committed) != 0 || len(source.Own) != 0 || source.HashIndexes != nil || source.PartialMask != nil {
			t.Fatal("denied source lost ownership or captured physical rows/indexes")
		}
		stage, err := exec.OpenNode(op.Ctx, &exec.State{Source: source}, b.Query.Root.(*plan.ProjectNode).Child)
		if err != nil {
			t.Fatal(err)
		}
		if stage.Source != source {
			t.Fatal("denied input lost its owned source")
		}
		selected, err := exec.SelectQuerySource(op.Ctx, source, 0, nil, nil, accessMethodHeapOperationsUnit(t, source.View.Data().Catalog))
		if err != nil || selected != source || selected.Visible != nil {
			t.Fatal("live selection revived denied input", err)
		}
	})
}
