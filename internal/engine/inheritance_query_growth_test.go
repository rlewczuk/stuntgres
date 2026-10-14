package engine

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestInheritanceQueryAdmissionGrowth(t *testing.T) {
	e := New()
	parentStage, p := queryCreateStage(t, e, "p", 1)
	if err := e.stageQueryTableRows(context.Background(), parentStage, p, [][]sqltype.Value{{inheritanceQueryInt(e, 10)}}, TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, parentStage)
	inherit := func(d *relationDescriptor) {
		d.inheritance = &tableInheritance{parents: []ObjectAddress{p.address}, columns: map[int32]inheritedColumn{d.attributes[0]: {origins: []inheritanceOrigin{{parent: p.address, attribute: p.attributes[0]}}}}, checks: map[string]inheritedConstraint{}, notNull: map[int32]inheritedConstraint{}}
	}
	childStage, c := queryCreateStage(t, e, "c", 1)
	inherit(c)
	if err := e.stageQueryTableRows(context.Background(), childStage, c, [][]sqltype.Value{{inheritanceQueryInt(e, 20)}}, TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, childStage)
	ref := func(d *relationDescriptor) ObjectRef {
		return ObjectRef{owner: e.owner, Epoch: e.CatalogRead().Epoch(), Address: d.address}
	}
	originalRef := ref(c)
	originalStorage := e.storage[c.address]
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
	if err = e.locks.acquire(holderOp.Context(), holder.ticket, lockResource{ObjectRef: originalRef}, accessExclusive); err != nil {
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
	parent := queryLeaf{ref: ref(p), relation: p, storage: e.storage[p.address]}
	scope.querySources = []queryLeaf{parent}
	if err = e.locks.acquire(op.Context(), scope.ticket, lockResource{ObjectRef: parent.ref}, accessShare); err != nil {
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
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: originalRef}]
	queued := entry != nil && len(entry.pending) == 1 && entry.pending[0].ticket == scope.ticket && entry.pending[0].mode == accessShare && entry.holders[holder.ticket].mode&accessExclusive != 0
	e.locks.mu.Unlock()
	if !queued {
		t.Fatal("scanner was not parked on the exact original descendant")
	}
	lateStage, late := queryCreateStage(t, e, "late", 1)
	inherit(late)
	if err = e.stageQueryTableRows(context.Background(), lateStage, late, [][]sqltype.Value{{inheritanceQueryInt(e, 9)}}, TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, lateStage)
	lateRef := ref(late)
	if lateRef == originalRef || e.storage[late.address] == originalStorage || e.storage[c.address] != originalStorage {
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
	admitted := map[ObjectRef]bool{}
	for _, leaf := range scope.inheritanceRelations {
		admitted[leaf.ref] = true
	}
	if len(admitted) != 2 || !admitted[originalRef] || !admitted[lateRef] {
		t.Fatal("late identity omitted from final physical manifest", admitted)
	}
	e.locks.mu.Lock()
	for _, physical := range []ObjectRef{originalRef, lateRef} {
		entry := e.locks.entries[lockResource{ObjectRef: physical}]
		if entry == nil || entry.holders[scope.ticket].mode&accessShare == 0 {
			e.locks.mu.Unlock()
			t.Fatal("final descendant grant missing", physical)
		}
	}
	e.locks.mu.Unlock()
	collectAuth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	collectAuth.recaptureCatalog(scope.inheritanceCatalog)
	e.mu.Lock()
	refs, err := e.queryTableRefsLocked(collectAuth, collectAuth.state.CurrentUser, scope.querySources, nil, op.session.token, queryRowMark{})
	var view *VisibilityView
	if err == nil {
		view, err = scope.acquireDataViewsLocked(e, refs)
	}
	var source *querySource
	if err == nil {
		source, err = captureInheritanceQueryPrefix(scope.inheritanceCatalog, view, parent, op.session.token)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	if len(refs) != 3 || view != scope.view || source.view.read() != view.read() {
		t.Fatal("growth did not use sole complete statement snapshot", refs)
	}
	if err = selectQuerySources(op.Context(), source); err != nil {
		t.Fatal(err)
	}
	got := map[int64]int{}
	for i := range source.count() {
		n, valueErr := source.row(i)[0].Integer()
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		got[n]++
	}
	if len(got) != 3 || got[10] != 1 || got[20] != 1 || got[9] != 1 {
		t.Fatal("wait reconciliation omitted late committed row", got)
	}
	for _, segment := range source.inheritance {
		if segment.prefix.view.read() != view.read() || &segment.prefix.committed[0] != &e.storage[segment.leaf.ref.Address].versions[0] {
			t.Fatal("capture copied roots or minted another snapshot")
		}
	}
	source.close()
	scope.Finish(nil)
	lockEmpty(t, e)
}
