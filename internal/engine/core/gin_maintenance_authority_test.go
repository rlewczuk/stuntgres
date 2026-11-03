package core_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"testing"
)

func ginMaintenanceForeignPresentation(t *testing.T) {
	e, a, b, oldIndex := ginCoreFixture(t)
	enginetest.SessionSQL(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(1,'{2,3}')")
	enginetest.SessionSQL(t, a, "SAVEPOINT accepted")
	oldRows := ginCoreWrites(a.Transactions, oldIndex.SQLIndex.Parent)
	oldOutput := oldRows[0].IndexOutputs[oldIndex.Address()]
	sourceState := oldRows[0].IndexAdmission
	if sourceState == nil || sourceState.Get(oldIndex.Address()) != oldOutput.Index {
		t.Fatal("producer lost actual admission catalog")
	}
	enginetest.SessionSQL(t, b, "ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_foreign_array")
	current := e.CatalogRead()
	target := ginCoreRelation(t, e, "gin_cohort_i")
	parent, _ := current.Relation(target.SQLIndex.Parent)
	prepare := func() *index.GinMaintenance {
		p := &index.GinMaintenance{Context: context.Background(), Index: target, CommittedIndex: target, Store: e.Storage.Relations[parent.Address()], View: current, TargetView: current}
		if err := p.Authorize(index.GinMaintenanceAuthority{View: current, Index: target, Parent: parent}); err != nil {
			t.Fatal(err)
		}
		return p
	}
	currentCopy := *target
	currentShaped := oldOutput
	currentShaped.Index = &currentCopy
	for _, invalidSource := range []struct {
		name      string
		state     *catalog.State
		wantError bool
	}{
		{"nil", nil, false},
		{"foreign", engine.New().CatalogRead().State(), true},
		{"mismatching", current.State(), true},
	} {
		t.Run("detached-current-shaped-"+invalidSource.name, func(t *testing.T) {
			_, _, accepted, err := prepare().OutputBindings(currentShaped, invalidSource.state)
			if accepted || (err != nil) != invalidSource.wantError {
				t.Fatal("semantic shape acquired produced-output authority", accepted, err)
			}
		})
	}
	exact := currentShaped
	exact.Index = target
	if _, actual, accepted, err := prepare().OutputBindings(exact, nil); err != nil || !accepted || actual != target {
		t.Fatal("lost exact enrolled current-pointer authority", accepted, err)
	}
	detached := *oldOutput.Index
	invalid := oldOutput
	invalid.Index = &detached
	if _, _, accepted, err := prepare().OutputBindings(invalid, sourceState); err == nil || accepted {
		t.Fatal("detached old pointer acquired source authority")
	}
	if _, _, accepted, err := prepare().OutputBindings(oldOutput, engine.New().CatalogRead().State()); err == nil || accepted {
		t.Fatal("foreign source catalog acquired authority")
	}
	if _, _, accepted, err := prepare().OutputBindings(oldOutput, nil); err != nil || accepted {
		t.Fatal("missing source catalog acquired presentation authority", err)
	}
	broken := catalog.CloneState(sourceState)
	oldView := current.WithState(sourceState)
	member, _ := oldView.FamilySupport(2745, 2277, 2277, 2)
	changed := *member
	changed.Function = 333
	changed.ObjectRevision++
	broken.SetDescriptor(changed.Address(), &changed)
	if _, _, accepted, err := prepare().OutputBindings(oldOutput, broken); err == nil || accepted {
		t.Fatal("incompatible executable source acquired authority")
	}
	ginCoreClean(t, b)
	mapped := ginCoreWrites(a.Transactions, parent.Address())
	ginCoreOutput(t, mapped, target, false)
	ginCoreOutput(t, oldRows, oldIndex, true)
	if &mapped[0].IndexOutputs[target.Address()].Keys[0] != &oldOutput.Keys[0] {
		t.Fatal("presentation cleanup reevaluated or copied produced keys")
	}
	enginetest.SessionSQL(t, a, "ROLLBACK TO accepted")
	ginCoreOutput(t, ginCoreWrites(a.Transactions, parent.Address()), target, false)
	enginetest.SessionSQL(t, a, "COMMIT")
	root := ginCorePending(t, e, target, 0, 1)
	if &root.Entries[0].Keys[0] != &oldOutput.Keys[0] {
		t.Fatal("commit reexecuted accepted source recipe")
	}
	if root.Gin == nil || storage.GinPendingIncidences(root.Gin) != 0 {
		t.Fatal("foreign presentation resurrected pending")
	}
}
