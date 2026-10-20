package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestInheritanceQueryAdmissionGrowth(t *testing.T) {
	e := New()
	parentStage, p := queryCreateStage(t, e, "p", 1)
	if err := e.stageQueryTableRows(context.Background(), parentStage, p, [][]sqltype.Value{{inheritanceQueryInt(e, 10)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, parentStage)
	inherit := func(d *catalog.Relation) {
		d.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{p.Address()}, Columns: map[int32]catalog.InheritedColumn{d.Attributes[0]: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: p.Attributes[0]}}}}, Checks: map[string]catalog.InheritedConstraint{}, NotNull: map[int32]catalog.InheritedConstraint{}}
	}
	childStage, c := queryCreateStage(t, e, "c", 1)
	inherit(c)
	if err := e.stageQueryTableRows(context.Background(), childStage, c, [][]sqltype.Value{{inheritanceQueryInt(e, 20)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, childStage)
	ref := func(d *catalog.Relation) ids.ObjectRef {
		return e.catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())
	}
	originalRef := ref(c)
	originalStorage := e.storage.Relations[c.Address()]
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
	if err = holder.gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	if err = e.locks.Acquire(holderOp.Context(), holder.ticket, lock.Resource{ObjectRef: originalRef}, lock.AccessExclusive); err != nil {
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
	if err = scope.gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	parent := exec.Leaf{Ref: ref(p), Relation: p, Storage: e.storage.Relations[p.Address()]}
	scope.querySources = []exec.Leaf{parent}
	if err = e.locks.Acquire(op.Context(), scope.ticket, lock.Resource{ObjectRef: parent.Ref}, lock.AccessShare); err != nil {
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
	lockState := e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: originalRef}]
	queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == scope.ticket && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[holder.ticket].Mode&lock.AccessExclusive != 0
	if !queued {
		t.Fatal("scanner was not parked on the exact original descendant")
	}
	lateStage, late := queryCreateStage(t, e, "late", 1)
	inherit(late)
	if err = e.stageQueryTableRows(context.Background(), lateStage, late, [][]sqltype.Value{{inheritanceQueryInt(e, 9)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, lateStage)
	lateRef := ref(late)
	if lateRef == originalRef || e.storage.Relations[late.Address()] == originalStorage || e.storage.Relations[c.Address()] != originalStorage {
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
	for _, leaf := range scope.inheritance.relations {
		admitted[leaf.Ref] = true
	}
	if len(admitted) != 2 || !admitted[originalRef] || !admitted[lateRef] {
		t.Fatal("late identity omitted from final physical manifest", admitted)
	}
	lockState = e.locks.Inspect()
	for _, physical := range []ids.ObjectRef{originalRef, lateRef} {
		entry := lockState.Resources[lock.Resource{ObjectRef: physical}]
		if entry == nil || entry.Holders[scope.ticket].Mode&lock.AccessShare == 0 {
			t.Fatal("final descendant grant missing", physical)
		}
	}
	collectAuth, err := operationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	collectAuth.Recapture(scope.inheritance.catalog)
	e.mu.Lock()
	refs, err := e.queryTableRefsLocked(collectAuth, collectAuth.State.CurrentUser, scope.querySources, nil, op.Session.token, plan.RowMark{})
	var view *visibility.View
	if err == nil {
		view, err = scope.acquireDataViewsLocked(e, refs)
	}
	var source *exec.Source
	if err == nil {
		source, err = captureInheritanceQueryPrefix(scope.inheritance.catalog, view, parent, op.Session.token)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if len(refs) != 3 || view != scope.view || source.View.Data() != view.Data() {
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
		if segment.Prefix.View.Data() != view.Data() || &segment.Prefix.Committed[0] != &e.storage.Relations[segment.Leaf.Ref.Address].Versions[0] {
			t.Fatal("capture copied roots or minted another snapshot")
		}
	}
	source.Close()
	scope.Finish(nil)
	lockEmpty(t, e)
}
