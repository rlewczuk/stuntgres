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
		enginetest.RoleExecute(t, e, op, q)
	}
	defer enginetest.RoleExecute(t, e, op, "ROLLBACK")
	enginetest.RoleExecute(t, e, op, "UPDATE t SET allowed=FALSE WHERE id<=2")
	enginetest.RoleExecute(t, e, op, "DELETE FROM t WHERE id=3")

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
	auth.Catalog = e.PrivateCatalogLocked(scope.Owner.Catalog)
	auth.Recapture(auth.Catalog)
	b, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	leaf := b.QuerySources[0]
	dataView, err := scope.AcquireDataViewLocked(e, leaf.Ref)
	reader := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_history_reader")
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	data := dataView.Data()
	address := leaf.Ref.Address
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
	if generation == nil || generation != leaf.Storage || data.Catalog.Epoch() != leaf.Ref.Epoch {
		t.Fatal("fixture lost actual storage/catalog generation")
	}
	descriptor := data.Catalog.MustRelation(address)
	if !descriptor.Security.RLSEnabled || !descriptor.Security.RLSForced {
		t.Fatal("fixture lacks actual published RLS flags")
	}
	for _, command := range []catalog.PolicyCommand{catalog.PolicyUpdate, catalog.PolicyDelete, catalog.PolicySelect} {
		decision, err := access.SelectRowSecurity(auth.Roles, descriptor, reader, command, false, false)
		if err != nil || !decision.Denied() {
			t.Fatal("effective ordinary principal is not denied", command, decision, err)
		}
		source, err := view.CaptureDeniedQuerySource(dataView, leaf.Ref)
		if err != nil {
			t.Fatal(err)
		}
		pin := source.View
		func() {
			defer source.Close()
			core.BorrowQueryAuthority(source, scope)
			if !source.RlsDenied || source.Ref != leaf.Ref || source.View == nil || source.View.Data() != data || source.View.Data().GenerationAt(address) != generation {
				t.Fatal("denied target lost retained identity/ownership/generation")
			}
			if source.Committed != nil || source.Own != nil || source.Visible != nil || source.HashIndexes != nil || source.PartialMask != nil {
				t.Fatal("denied target captured physical prefixes or live selection")
			}
			selected, err := exec.SelectQuerySource(op.Ctx, source, data.FrontierAt(address), committedEnd, privateEnd, accessMethodHeapOperationsUnit(t, data.Catalog))
			if err != nil || selected != source || source.Visible != nil || source.Committed != nil || source.Own != nil {
				t.Fatal("hidden history revived denied target", err)
			}
			stage, err := exec.OpenNode(op.Ctx, &exec.State{Source: source}, b.Query.Root.(*plan.ProjectNode).Child)
			// The underlying fixture source has id and allowed; the outer
			// projection narrows its output, not the owned base-source schema.
			if err != nil || stage.Source != source || len(stage.Columns) != 2 {
				t.Fatal("denied target lost typed stage ownership", err)
			}
		}()
		if pin.Load() != nil {
			t.Fatal("closing denied target retained its visibility pin")
		}
		if dataView.Data() != data || data.Ends[address] != committedEnd || data.Deleted[address] != privateEnd || data.GenerationAt(address) != generation {
			t.Fatal("denied close changed shared roots/generation")
		}
	}
	ordinary, err := exec.CaptureSource(op.Ctx, dataView, leaf.Ref)
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Close()
	if len(ordinary.Committed) < 4 || len(ordinary.Own) < 2 || len(ordinary.Visible) != 2 || ordinary.RlsDenied {
		t.Fatal("denied captures corrupted ordinary selection of actual history")
	}
}
