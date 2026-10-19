package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	owner, reader, bypass, member := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_unit_owner"), e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_unit_reader"), e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_unit_bypass"), e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_unit_member")
	using, err := policyUnitBind(t, e, op, d, "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	next := *d
	next.Owner = owner
	check := func(label string, p authorization.PrincipalRef, command catalog.PolicyCommand, proposed bool, want rlsDisposition) {
		t.Helper()
		got, err := access.RowSecurityDecision(auth.Roles, &next, p, command, proposed)
		if err != nil || got != want {
			t.Fatalf("%s got %v,%v want %v", label, got, err, want)
		}
	}
	check("disabled", member, catalog.PolicySelect, false, rlsUnrestricted)
	next.Security.RLSEnabled = true
	check("owner", owner, catalog.PolicySelect, false, rlsUnrestricted)
	check("inherited owner", reader, catalog.PolicySelect, false, rlsUnrestricted)
	next.Security.RLSForced = true
	check("forced owner", owner, catalog.PolicySelect, false, rlsDefaultDeny)
	check("direct bypass", bypass, catalog.PolicySelect, false, rlsUnrestricted)
	check("attribute not inherited", member, catalog.PolicySelect, false, rlsDefaultDeny)
	check("direct superuser", d.Owner, catalog.PolicySelect, false, rlsUnrestricted)
	next.Security.Policies = []catalog.PolicyDefinition{{Permissive: false, Command: catalog.PolicyAll, Roles: []catalog.PolicyRole{{Public: true}}, Using: policyPayloadOf(using)}}
	check("restrictive only", member, catalog.PolicySelect, false, rlsDefaultDeny)
	next.Security.Policies[0].Permissive = true
	next.Security.Policies[0].Roles = []catalog.PolicyRole{{Ref: owner}}
	check("role inapplicable", member, catalog.PolicySelect, false, rlsDefaultDeny)
	check("inherited policy", reader, catalog.PolicySelect, false, rlsNeedsExpressions)
	next.Security.Policies[0].Roles = []catalog.PolicyRole{{Public: true}}
	check("public constant is not a shortcut", member, catalog.PolicySelect, false, rlsNeedsExpressions)
	check("ALL check falls back", member, catalog.PolicyInsert, true, rlsNeedsExpressions)
	next.Security.Policies[0].Command = catalog.PolicySelect
	check("command inapplicable", member, catalog.PolicyInsert, true, rlsDefaultDeny)
	got, err := access.RowSecurityGroups(auth.Roles, &next, member, catalog.PolicyUpdate, false, true)
	if err != nil || got != rlsDefaultDeny {
		t.Fatal("same-phase false conjunction", got, err)
	}
	next.Security.Policies[0].Using = nil
	check("omitted using", member, catalog.PolicySelect, false, rlsDefaultDeny)
	next.Security.Policies[0].Command = catalog.PolicyInsert
	next.Security.Policies[0].Check = policyPayloadOf(using)
	check("explicit check", member, catalog.PolicyInsert, true, rlsNeedsExpressions)
	got, err = access.RowSecurityGroups(auth.Roles, &next, member, catalog.PolicyInsert, true, true)
	if err != nil || got != rlsDefaultDeny {
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
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		b, err := e.describe(op.ctx, ast, auth, scope.lockedRef, scope.input, op, nil, &scope.queryLockAdmission)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		ref := b.querySources[0].ref
		view, err := scope.acquireDataViewLocked(e, ref)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		source, err := captureDeniedQuerySource(view, ref)
		if err != nil {
			t.Fatal(err)
		}
		source.borrowAuthority(scope)
		defer source.close()
		if source.view == nil || !source.rlsDenied || len(source.committed) != 0 || len(source.own) != 0 || source.hashIndexes != nil || source.partialMask != nil {
			t.Fatal("denied source lost ownership or captured physical rows/indexes")
		}
		stage, err := openQueryNode(op.ctx, &queryState{source: source}, b.query.Root.(*projectNode).child)
		if err != nil {
			t.Fatal(err)
		}
		if stage.source != source {
			t.Fatal("denied input lost its owned source")
		}
		selected, err := selectQuerySource(op.ctx, source, 0, nil, nil)
		if err != nil || selected != source || selected.visible != nil {
			t.Fatal("live selection revived denied input", err)
		}
	})
}

func TestRLSBaseFlagsAndPublication(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := e.CatalogRead()
	noOpScope := &StatementScope{op: op}
	e.mu.Lock()
	if _, err := e.stageRowSecurityFlagsLocked(noOpScope, op, auth, d, d); err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().State() != retained.State() {
		t.Fatal("no-op published a catalog revision")
	}
	exhausted := *d
	exhausted.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	exhaustedNext := rowSecurityFlags(&exhausted, []parser.AlterTableAction{{Action: parser.EnableRowSecurity}})
	exhaustedScope := &StatementScope{op: op, owner: &transactionOwner{}}
	e.mu.Lock()
	if _, err := e.stageRowSecurityFlagsLocked(exhaustedScope, op, auth, &exhausted, exhaustedNext); err == nil || err.Error() != "stuntgres: definition revision exhausted" {
		t.Fatal("revision exhaustion", err)
	}
	if e.CatalogRead().State() != retained.State() || exhaustedScope.catalog != nil {
		t.Fatal("revision exhaustion staged or published changes")
	}
	expression, err := policyUnitBind(t, e, op, d, "id>0")
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	oid, err := e.catalog.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	old := *d
	old.Security.Policies = []catalog.PolicyDefinition{{OID: oid, Name: "retained", Permissive: true, Command: catalog.PolicyAll, Roles: []catalog.PolicyRole{{Ref: d.Owner}}, Using: policyPayloadOf(expression), Check: policyPayloadOf(expression)}}
	actions := []parser.AlterTableAction{{Action: parser.EnableRowSecurity}, {Action: parser.ForceRowSecurity}, {Action: parser.DisableRowSecurity}}
	next := rowSecurityFlags(&old, actions)
	if next == &old || next.Security.RLSEnabled || !next.Security.RLSForced || old.Security.RLSEnabled || old.Security.RLSForced {
		t.Fatal("independent flags/old snapshot")
	}
	if &next.Security.Policies[0] != &old.Security.Policies[0] || policyExpressionOf(next.Security.Policies[0].Using) != expression || policyExpressionOf(next.Security.Policies[0].Check) != expression || next.Security.Policies[0].OID != oid {
		t.Fatal("flag action copied or changed immutable policy payload")
	}
	if rowSecurityFlags(next, []parser.AlterTableAction{{Action: parser.DisableRowSecurity}, {Action: parser.ForceRowSecurity}}) != next {
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
			if copy.Security.RLSEnabled || !copy.Security.RLSForced || len(copy.Security.Policies) != 1 || policyExpressionOf(copy.Security.Policies[0].Using) == expression {
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
		groups, err := rowSecurityDumpGroups(context.Background(), e.catalog.read(catalog), e.roles.read(e.catalog.owner(), e.catalog.read(catalog), e.roles.published))
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
		if strings.Contains(groups[0].sql, "ENABLE") != state.enabled || strings.Contains(groups[0].sql, "FORCE") != state.forced {
			t.Fatal("serialized flags", groups[0].sql)
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
	next := rowSecurityFlags(d, []parser.AlterTableAction{{Action: parser.EnableRowSecurity}, {Action: parser.ForceRowSecurity}})
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
