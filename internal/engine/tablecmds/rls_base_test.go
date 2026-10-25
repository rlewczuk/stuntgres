package tablecmds_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

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
	noOpScope := &core.StatementScope{Op: op}
	e.Mu.Lock()
	if _, err := tablecmds.StageRowSecurityFlagsLocked(e, noOpScope, op, auth, d, d); err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().State() != retained.State() {
		t.Fatal("no-op published a catalog revision")
	}
	exhausted := *d
	exhausted.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	exhaustedNext := access.RowSecurityFlags(&exhausted, []parser.AlterTableAction{{Action: parser.EnableRowSecurity}})
	exhaustedScope := &core.StatementScope{Op: op, Owner: &session.TransactionOwner{}}
	e.Mu.Lock()
	if _, err := tablecmds.StageRowSecurityFlagsLocked(e, exhaustedScope, op, auth, &exhausted, exhaustedNext); err == nil || err.Error() != "stuntgres: definition revision exhausted" {
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
	if _, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	imported := engine.New()
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
	core.SetPreparedCatalogPublicationCtx(prepared, ctx)
	if _, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err == nil {
		t.Fatal("canceled flags published")
	}
	if e.CatalogRead().State() != base.State() || d.Security.RLSEnabled || d.Security.RLSForced {
		t.Fatal("cancellation changed original catalog")
	}
	rlsBaseLockCancellation(t, e, d)
}

func policyUnitBind(t *testing.T, e *core.Engine, op *session.Operation, d *catalog.Relation, condition string) (*stored.Policy, error) {
	t.Helper()
	raw := enginetest.Statement(t, "CREATE POLICY p ON t USING("+condition+")").(*parser.CreatePolicy)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	return engine.ViewHost{tablecmds.TableHost{core.DDLHost{E: e, Op: op}}}.BindPolicyExpression(auth, d, raw.Using, sqltype.InputEnvironment{})
}

func rlsBaseLockCancellation(t *testing.T, e *core.Engine, d *catalog.Relation) {
	t.Helper()
	holder := sessionStart(t, e, nil)
	waiter := sessionStart(t, e, nil)
	tx := transactionBegin(t, holder)
	ref := catalogRef(t, e, e.CatalogRead(), d.Name())
	if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		transactionEnd(t, holder, tx, false)
		if !joined {
			lockAwait(t, done, context.Canceled)
		}
	}()
	go func() {
		op, err := waiter.Acquire(ctx)
		if err == nil {
			_, err = e.Execute(op, &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}, Actions: []parser.AlterTableAction{{Action: parser.DisableRowSecurity}}}, 0, nil)
			op.Done()
		}
		done <- err
	}()
	lockPending(t, e, 1)
	cancel()
	lockAwait(t, done, context.Canceled)
	joined = true
	current := e.CatalogRead().MustRelation(d.Address())
	if current != d || current.Security.RLSEnabled || current.Security.RLSForced {
		t.Fatal("canceled no-op changed immutable descriptor")
	}
}
