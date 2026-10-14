package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

// Transactional permanent CREATE remains outside the public native contract.
// The existing grant/catalog publication harness exercises the real SELECT
// admission seam with different physical identities, storage and graph edges.
func TestInheritanceQueryAdmissionReplacement(t *testing.T) {
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
	oldRef := ObjectRef{owner: e.owner, Epoch: e.CatalogRead().Epoch(), Address: c.address}
	oldStorage := e.storage[c.address]
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
	if err = e.locks.acquire(holderOp.Context(), holder.ticket, lockResource{ObjectRef: oldRef}, accessExclusive); err != nil {
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
	parentRef := ObjectRef{owner: e.owner, Epoch: e.CatalogRead().Epoch(), Address: p.address}
	parent := queryLeaf{ref: parentRef, relation: p, storage: e.storage[p.address]}
	scope.querySources = []queryLeaf{parent}
	if err = e.locks.acquire(op.Context(), scope.ticket, lockResource{ObjectRef: parentRef}, accessShare); err != nil {
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
				t.Error("admitted descendant worker did not join")
			}
		}
		scope.Finish(err)
	}()
	lockPending(t, e, 1)
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: oldRef}]
	queued := entry != nil && len(entry.pending) == 1 && entry.pending[0].ticket == scope.ticket && entry.pending[0].mode == accessShare && entry.holders[holder.ticket].mode&accessExclusive != 0
	e.locks.mu.Unlock()
	if !queued {
		t.Fatal("SELECT did not wait on admitted original descendant identity")
	}
	remove := e.BeginCatalogStage(e.CatalogRead())
	if err = remove.Remove(oldRef); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, remove)
	replacementStage, replacement := queryCreateStage(t, e, "c", 1)
	inherit(replacement)
	if err = e.stageQueryTableRows(context.Background(), replacementStage, replacement, [][]sqltype.Value{{inheritanceQueryInt(e, 9)}}, TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, replacementStage)
	if replacement.address == c.address || e.storage[replacement.address] == oldStorage || e.storage[c.address] != nil {
		t.Fatal("replacement did not change physical identity and storage")
	}
	if children := inheritanceChildren(e.CatalogRead(), p.address); len(children) != 1 || children[0] != replacement {
		t.Fatal("replacement graph not published")
	}
	if err = holder.Finish(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("descendant wait did not resume")
	}
	if !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("in-flight SELECT retargeted a same-name sentinel", err)
	}
	scope.Finish(err)
	lockEmpty(t, e)
	freshOp, freshDone, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer freshDone()
	fresh, err := e.StartStatement(freshOp)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Finish(nil)
	if err = fresh.gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	fresh.querySources = []queryLeaf{parent}
	if err = e.locks.acquire(freshOp.Context(), fresh.ticket, lockResource{ObjectRef: parentRef}, accessShare); err != nil {
		t.Fatal(err)
	}
	if err = fresh.acquireInheritanceSources(nil); err != nil {
		t.Fatal(err)
	}
	collectAuth, err := freshOp.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	collectAuth.recaptureCatalog(fresh.inheritanceCatalog)
	e.mu.Lock()
	refs, err := e.queryTableRefsLocked(collectAuth, collectAuth.state.CurrentUser, fresh.querySources, nil, freshOp.session.token, queryRowMark{})
	var view *VisibilityView
	if err == nil {
		view, err = fresh.acquireDataViewsLocked(e, refs)
	}
	var source *querySource
	if err == nil {
		source, err = captureInheritanceQueryPrefix(fresh.inheritanceCatalog, view, parent, freshOp.session.token)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	if err = selectQuerySources(freshOp.Context(), source); err != nil {
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
	if len(got) != 2 || got[9] != 1 || got[10] != 1 {
		t.Fatal("fresh admitted scan did not use new sentinel identity", got)
	}
	source.close()
	fresh.Finish(nil)
	lockEmpty(t, e)
}
