package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"testing"
)

func TestRLSBaseDeniedTargetHistory(t *testing.T) {
	e, op, done, _ := policyUnitFixture(t)
	defer done()
	// SQL supplies real committed and transaction-local physical history only.
	// Assertions below exercise metadata decisions and denied capture directly.
	for _, q := range []string{
		"CREATE ROLE rls_history_reader",
		"GRANT SELECT,UPDATE,DELETE ON t TO rls_history_reader",
		"INSERT INTO t VALUES(1,TRUE),(2,TRUE),(3,TRUE),(4,TRUE)",
		"DELETE FROM t WHERE id=4",
		"ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY",
		"BEGIN",
	} {
		roleExecute(t, e, op, q)
	}
	defer roleExecute(t, e, op, "ROLLBACK")
	roleExecute(t, e, op, "UPDATE t SET allowed=FALSE WHERE id<=2")
	roleExecute(t, e, op, "DELETE FROM t WHERE id=3")

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
	auth.Catalog = e.privateCatalogLocked(scope.owner.catalog)
	auth.Recapture(auth.Catalog)
	b, err := e.describe(op.ctx, ast, auth, scope.lockedRef, scope.input, op, nil, &scope.queryLockAdmission)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	leaf := b.querySources[0]
	view, err := scope.acquireDataViewLocked(e, leaf.ref)
	reader := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_history_reader")
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	data := view.Data()
	address := leaf.ref.Address
	committedEnd, privateEnd := data.Ends[address], data.Deleted[address]
	if len(data.Committed[address]) < 4 || committedEnd == nil || len(committedEnd.Rows) == 0 || privateEnd == nil || len(privateEnd.Rows) == 0 {
		t.Fatal("fixture lacks actual committed/private deletion history")
	}
	replacements := 0
	for _, row := range data.Own[address] {
		if row.Base != 0 {
			replacements++
		}
	}
	if replacements < 2 {
		t.Fatal("fixture lacks actual multiple private replacement versions", replacements)
	}
	generation := data.GenerationAt(address)
	if generation == nil || generation != leaf.storage || data.Catalog.Epoch() != leaf.ref.Epoch {
		t.Fatal("fixture lost actual storage/catalog generation")
	}
	descriptor := data.Catalog.MustRelation(address)
	if !descriptor.Security.RLSEnabled || !descriptor.Security.RLSForced {
		t.Fatal("fixture lacks actual published RLS flags")
	}
	for _, command := range []catalog.PolicyCommand{catalog.PolicyUpdate, catalog.PolicyDelete, catalog.PolicySelect} {
		decision, err := access.RowSecurityDecision(auth.Roles, descriptor, reader, command, false)
		if err != nil || decision != access.RLSDefaultDeny {
			t.Fatal("effective ordinary principal is not denied", command, decision, err)
		}
		source, err := captureDeniedQuerySource(view, leaf.ref)
		if err != nil {
			t.Fatal(err)
		}
		pin := source.view
		func() {
			defer source.close()
			source.borrowAuthority(scope)
			if !source.rlsDenied || source.ref != leaf.ref || source.view == nil || source.view.Data() != data || source.view.Data().GenerationAt(address) != generation {
				t.Fatal("denied target lost retained identity/ownership/generation")
			}
			if source.committed != nil || source.own != nil || source.visible != nil || source.hashIndexes != nil || source.partialMask != nil {
				t.Fatal("denied target captured physical prefixes or live selection")
			}
			selected, err := selectQuerySource(op.ctx, source, data.FrontierAt(address), committedEnd, privateEnd)
			if err != nil || selected != source || source.visible != nil || source.committed != nil || source.own != nil {
				t.Fatal("hidden history revived denied target", err)
			}
			stage, err := openQueryNode(op.ctx, &queryState{source: source}, b.query.Root.(*projectNode).Child)
			// The underlying fixture source has id and allowed; the outer
			// projection narrows its output, not the owned base-source schema.
			if err != nil || stage.source != source || len(stage.schema) != 2 {
				t.Fatal("denied target lost typed stage ownership", err)
			}
		}()
		if pin.Load() != nil {
			t.Fatal("closing denied target retained its visibility pin")
		}
		if view.Data() != data || data.Ends[address] != committedEnd || data.Deleted[address] != privateEnd || data.GenerationAt(address) != generation {
			t.Fatal("denied close changed shared roots/generation")
		}
	}
	ordinary, err := captureQuerySource(op.ctx, view, leaf.ref)
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.close()
	if len(ordinary.committed) < 4 || len(ordinary.own) < 2 || len(ordinary.visible) != 2 || ordinary.rlsDenied {
		t.Fatal("denied captures corrupted ordinary selection of actual history")
	}
}
