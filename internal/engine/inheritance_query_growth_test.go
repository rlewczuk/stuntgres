package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestInheritanceQueryAdmissionGrowth(t *testing.T) {
	e := New()
	parentStage, p := queryCreateStage(t, e, "p", 1)
	if err := e.StageQueryTableRows(context.Background(), parentStage, p, [][]sqltype.Value{{inheritanceQueryInt(e, 10)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, parentStage)
	inherit := func(d *catalog.Relation) {
		d.Inheritance = &catalog.TableInheritance{Parents: inheritanceUnitParents(p.Address()), Columns: map[int32]catalog.InheritedColumn{d.Attributes[0]: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: p.Attributes[0]}}}}, Checks: map[string]catalog.InheritedConstraint{}, NotNull: map[int32]catalog.InheritedConstraint{}}
	}
	childStage, c := queryCreateStage(t, e, "c", 1)
	inherit(c)
	if err := e.StageQueryTableRows(context.Background(), childStage, c, [][]sqltype.Value{{inheritanceQueryInt(e, 20)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, childStage)
	ref := func(d *catalog.Relation) ids.ObjectRef {
		return e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())
	}
	originalRef := ref(c)
	originalStorage := e.Storage.Relations[c.Address()]
	holderOp, holderDone, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holderDone()
	holder, err := e.StartStatement(holderOp)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Finish(nil)
	if err = holder.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	if err = e.Locks.Acquire(holderOp.Context(), holder.LockTicket, lock.Resource{ObjectRef: originalRef}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	parent := exec.Leaf{Ref: ref(p), Relation: p, Storage: e.Storage.Relations[p.Address()]}
	scope.QuerySources = []exec.Leaf{parent}
	if err = e.Locks.Acquire(op.Context(), scope.LockTicket, lock.Resource{ObjectRef: parent.Ref}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	joined := false
	result := make(chan error, 1)
	go func() { result <- scope.acquireInheritanceSources(nil) }()
	defer func() {
		cancel()
		holder.Finish(nil)
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("growth admission worker did not join")
			}
		}
		scope.Finish(err)
	}()
	lockPending(t, e, 1)
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: originalRef}]
	queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == scope.LockTicket && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[holder.LockTicket].Mode&lock.AccessExclusive != 0
	if !queued {
		t.Fatal("scanner was not parked on the exact original descendant")
	}
	lateStage, late := queryCreateStage(t, e, "late", 1)
	inherit(late)
	if err = e.StageQueryTableRows(context.Background(), lateStage, late, [][]sqltype.Value{{inheritanceQueryInt(e, 9)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, lateStage)
	lateRef := ref(late)
	if lateRef == originalRef || e.Storage.Relations[late.Address()] == originalStorage || e.Storage.Relations[c.Address()] != originalStorage {
		t.Fatal("growth did not preserve original and add a distinct physical relation")
	}
	if err = holder.Finish(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("growth admission did not resume")
	}
	if err != nil {
		t.Fatal(err)
	}
	admitted := map[ids.ObjectRef]bool{}
	for _, leaf := range scope.Inheritance.relations {
		admitted[leaf.Ref] = true
	}
	if len(admitted) != 2 || !admitted[originalRef] || !admitted[lateRef] {
		t.Fatal("late identity omitted from final physical manifest", admitted)
	}
	lockState = e.Locks.Inspect()
	for _, physical := range []ids.ObjectRef{originalRef, lateRef} {
		entry := lockState.Resources[lock.Resource{ObjectRef: physical}]
		if entry == nil || entry.Holders[scope.LockTicket].Mode&lock.AccessShare == 0 {
			t.Fatal("final descendant grant missing", physical)
		}
	}
	collectAuth, err := OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	collectAuth.Recapture(scope.Inheritance.catalog)
	e.Mu.Lock()
	refs, err := e.QueryTableRefsLocked(collectAuth, collectAuth.State.CurrentUser, scope.QuerySources, nil, op.Session.SessionToken, plan.RowMark{})
	var view *visibility.View
	if err == nil {
		view, err = scope.AcquireDataViewsLocked(e, refs)
	}
	var source *exec.Source
	if err == nil {
		source, err = table.CaptureInheritanceQueryPrefix(scope.Inheritance.catalog, view, parent, op.Session.SessionToken)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if len(refs) != 3 || view != scope.View || source.View.Data() != view.Data() {
		t.Fatal("growth did not use sole complete statement snapshot", refs)
	}
	if err = exec.SelectQuerySources(op.Context(), source); err != nil {
		t.Fatal(err)
	}
	got := map[int64]int{}
	for i := range source.Count() {
		n, valueErr := source.Row(i)[0].Integer()
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		got[n]++
	}
	if len(got) != 3 || got[10] != 1 || got[20] != 1 || got[9] != 1 {
		t.Fatal("wait reconciliation omitted late committed row", got)
	}
	for _, segment := range source.Inheritance {
		if segment.Prefix.View.Data() != view.Data() || &segment.Prefix.Committed[0] != &e.Storage.Relations[segment.Leaf.Ref.Address].Versions[0] {
			t.Fatal("capture copied roots or minted another snapshot")
		}
	}
	source.Close()
	scope.Finish(nil)
	lockEmpty(t, e)
}
