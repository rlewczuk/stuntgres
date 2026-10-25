package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"testing"
)

func TestRLSBaseDecision(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	// Incidental internal catalog setup; assertions target the private metadata
	// decision function, never public SQL results.
	for _, q := range []string{"CREATE ROLE rls_unit_owner", "CREATE ROLE rls_unit_reader", "CREATE ROLE rls_unit_bypass BYPASSRLS", "CREATE ROLE rls_unit_member", "GRANT rls_unit_bypass TO rls_unit_member", "GRANT rls_unit_owner TO rls_unit_reader WITH INHERIT TRUE"} {
		roleExecute(t, e, op, q)
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
		ast := statement(t, "SELECT id FROM t")
		if _, err := scope.Describe(ast); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		b, err := Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
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
		borrowQueryAuthority(source, scope)
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

func TestRLSBaseFlagsAndPublication(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := e.CatalogRead()
	noOpScope := &StatementScope{Op: op}
	e.Mu.Lock()
	if _, err := stageRowSecurityFlagsLocked(e, noOpScope, op, auth, d, d); err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().State() != retained.State() {
		t.Fatal("no-op published a catalog revision")
	}
	exhausted := *d
	exhausted.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	exhaustedNext := access.RowSecurityFlags(&exhausted, []parser.AlterTableAction{{Action: parser.EnableRowSecurity}})
	exhaustedScope := &StatementScope{Op: op, Owner: &session.TransactionOwner{}}
	e.Mu.Lock()
	if _, err := stageRowSecurityFlagsLocked(e, exhaustedScope, op, auth, &exhausted, exhaustedNext); err == nil || err.Error() != "stuntgres: definition revision exhausted" {
		t.Fatal("revision exhaustion", err)
	}
	if e.CatalogRead().State() != retained.State() || exhaustedScope.Catalog != nil {
		t.Fatal("revision exhaustion staged or published changes")
	}
	expression, err := policyUnitBind(t, e, op, d, "id>0")
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	old := *d
	old.Security.Policies = []catalog.PolicyDefinition{{OID: oid, Name: "retained", Permissive: true, Command: catalog.PolicyAll, Roles: []catalog.PolicyRole{{Ref: d.Owner}}, Using: view.PolicyPayloadOf(expression), Check: view.PolicyPayloadOf(expression)}}
	actions := []parser.AlterTableAction{{Action: parser.EnableRowSecurity}, {Action: parser.ForceRowSecurity}, {Action: parser.DisableRowSecurity}}
	next := access.RowSecurityFlags(&old, actions)
	if next == &old || next.Security.RLSEnabled || !next.Security.RLSForced || old.Security.RLSEnabled || old.Security.RLSForced {
		t.Fatal("independent flags/old snapshot")
	}
	if &next.Security.Policies[0] != &old.Security.Policies[0] || stored.PolicyOf(next.Security.Policies[0].Using) != expression || stored.PolicyOf(next.Security.Policies[0].Check) != expression || next.Security.Policies[0].OID != oid {
		t.Fatal("flag action copied or changed immutable policy payload")
	}
	if access.RowSecurityFlags(next, []parser.AlterTableAction{{Action: parser.DisableRowSecurity}, {Action: parser.ForceRowSecurity}}) != next {
		t.Fatal("no-op copied descriptor")
	}
	next.ObjectRevision++
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	imported := New()
	if _, err := imported.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range imported.CatalogRead().Descriptors() {
		copy, ok := descriptor.(*catalog.Relation)
		if ok && copy.Name() == "t" {
			if copy.Security.RLSEnabled || !copy.Security.RLSForced || len(copy.Security.Policies) != 1 || stored.PolicyOf(copy.Security.Policies[0].Using) == expression {
				t.Fatal("remap lost flags/policy or shared original expression")
			}
			return
		}
	}
	t.Fatal("remap lost relation")
}

func TestRLSBaseControls(t *testing.T) {
	e, _, done, d := policyUnitFixture(t)
	defer done()
	base := e.CatalogRead()
	for _, state := range []struct{ enabled, forced bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		next := *d
		next.Security.RLSEnabled, next.Security.RLSForced = state.enabled, state.forced
		catalog := catalog.CloneState(base.State())
		catalog.SetDescriptor(d.Address(), &next)
		groups, err := stored.RowSecurityDumpGroups(context.Background(), e.Catalog.Read(catalog), e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Read(catalog)))
		if err != nil {
			t.Fatal(err)
		}
		if !state.enabled && !state.forced {
			if len(groups) != 0 {
				t.Fatal("default state serialized")
			}
			continue
		}
		if len(groups) != 1 {
			t.Fatal("protected policy-less table not serialized", groups)
		}
		if strings.Contains(groups[0].SQL, "ENABLE") != state.enabled || strings.Contains(groups[0].SQL, "FORCE") != state.forced {
			t.Fatal("serialized flags", groups[0].SQL)
		}
		if next.Security.RLSEnabled != state.enabled || next.Security.RLSForced != state.forced {
			t.Fatal("Dump mutated descriptor")
		}
	}
}

func TestRLSBaseCancellationAndLocks(t *testing.T) {
	e, _, done, d := policyUnitFixture(t)
	defer done()
	base := e.CatalogRead()
	next := access.RowSecurityFlags(d, []parser.AlterTableAction{{Action: parser.EnableRowSecurity}, {Action: parser.ForceRowSecurity}})
	next.ObjectRevision++
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prepared.ctx = ctx
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err == nil {
		t.Fatal("canceled flags published")
	}
	if e.CatalogRead().State() != base.State() || d.Security.RLSEnabled || d.Security.RLSForced {
		t.Fatal("cancellation changed original catalog")
	}
	rlsBaseLockCancellation(t, e, d)
}
