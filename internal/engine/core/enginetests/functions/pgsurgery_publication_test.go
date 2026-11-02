package functions_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func surgeryPublicationFixture(t *testing.T) (*core.Engine, ids.ObjectRef, plan.InstalledPgsurgeryExpr, sqltype.Value) {
	t.Helper()
	e := engine.New()
	for _, q := range []string{"CREATE EXTENSION pg_surgery", "CREATE TABLE surgery_heap(n integer)", "INSERT INTO surgery_heap VALUES(1)"} {
		enginetest.Run(t, e, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "surgery_heap")
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	node, err := binder.BindFunctionProvider(enginetest.FunctionCall("heap_force_freeze", enginetest.StringLiteral("-"), enginetest.StringLiteral("{}")))
	if err != nil {
		t.Fatal(err)
	}
	array, ok := e.CatalogRead().LookupType(sqltype.ID(1010))
	if !ok {
		t.Fatal("missing native tid array")
	}
	row := e.Storage.Relations[ref.Address].Versions[0]
	block, offset, err := storage.LogicalTID(row.Version)
	if err != nil {
		t.Fatal(err)
	}
	tid, err := sqltype.TIDValue(array.Element(), block, offset)
	if err != nil {
		t.Fatal(err)
	}
	input, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{tid})
	if err != nil {
		t.Fatal(err)
	}
	return e, ref, node.(plan.InstalledPgsurgeryExpr), input
}

func TestPgsurgeryFreezePublication(t *testing.T) {
	e, ref, node, input := surgeryPublicationFixture(t)
	observer := enginetest.StartSession(t, e, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	op, err := observer.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			scope.Finish(nil)
		}
	}()
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	scope.Owner.Mu.Lock()
	e.Mu.Lock()
	store := e.Storage.Relations[ref.Address]
	before := store.Versions[0]
	generation := store.Generation
	view, err := scope.CaptureMaintenanceDataViewsLocked(e, []ids.ObjectRef{ref})
	e.Mu.Unlock()
	scope.Owner.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()
	if err := core.HostOf(scope).Contrib().Pgsurgery(node, uint32(ref.Address.ObjectID), input); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	header := store.Headers
	after := header.Apply(before)
	if store.Generation != generation+1 || after.Version != before.Version || after.Row != before.Row || after.HeapXmin() != 2 || after.HeapXmax() != 0 || &after.Values[0] != &before.Values[0] {
		e.Mu.Unlock()
		t.Fatal("repair did not publish exact shared header", after)
	}
	if store.Versions[0].ForcedFrozen || view.Data().Headers[ref.Address] != nil || view.Data().Committed[ref.Address][0].ForcedFrozen {
		e.Mu.Unlock()
		t.Fatal("captured old root mutated")
	}
	e.Mu.Unlock()
	old, err := exec.CaptureSource(t.Context(), view, ref)
	if err != nil {
		t.Fatal(err)
	}
	if old.VersionAt(old.RawIndex(0)).HeapXmin() != uint32(before.InsertFullXID) {
		t.Fatal("old data capture rebound to new repair header")
	}
	old.Close()
	// Cancellation is real and occurs after the completed publication, before
	// the enclosing owner's disposal. No callback or fabricated stage observes it.
	cancel()
	err = scope.Finish(ctx.Err())
	finished = true
	if !errors.Is(err, context.Canceled) {
		t.Fatal("late cancellation outcome", err)
	}
	e.Mu.Lock()
	if store.Headers != header || store.Headers.Apply(before).HeapXmin() != 2 {
		e.Mu.Unlock()
		t.Fatal("owner abort rolled back completed repair")
	}
	e.Mu.Unlock()
}

func TestPgsurgeryFreezePrepublication(t *testing.T) {
	e, ref, node, _ := surgeryPublicationFixture(t)
	observer := enginetest.StartSession(t, e, nil)
	scope, finish := visibilityScope(t, e, observer)
	array, _ := e.CatalogRead().LookupType(sqltype.ID(1010))
	input, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{sqltype.Null(array.Element())})
	if err != nil {
		t.Fatal(err)
	}
	store := e.Storage.Relations[ref.Address]
	generation, root := store.Generation, store.Headers
	err = core.HostOf(scope).Contrib().Pgsurgery(node, 0, input)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22004" || diagnostic.Message != "array must not contain nulls" {
		t.Fatal("array admission order", err)
	}
	if store.Generation != generation || store.Headers != root {
		t.Fatal("prepublication error mutated heap")
	}
	if err := finish(err); err == nil {
		t.Fatal("failed enclosing statement succeeded")
	}
}
