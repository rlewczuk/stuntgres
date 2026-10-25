package engine

import (
	"context"
	"errors"
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

// Transactional permanent CREATE remains outside the public native contract.
// The existing grant/catalog publication harness exercises the real SELECT
// admission seam with different physical identities, storage and graph edges.
func TestInheritanceQueryAdmissionReplacement(t *testing.T) {
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
	oldRef := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), c.Address())
	oldStorage := e.Storage.Relations[c.Address()]
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
	if err = e.Locks.Acquire(holderOp.Context(), holder.LockTicket, lock.Resource{ObjectRef: oldRef}, lock.AccessExclusive); err != nil {
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
	parentRef := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), p.Address())
	parent := exec.Leaf{Ref: parentRef, Relation: p, Storage: e.Storage.Relations[p.Address()]}
	scope.QuerySources = []exec.Leaf{parent}
	if err = e.Locks.Acquire(op.Context(), scope.LockTicket, lock.Resource{ObjectRef: parentRef}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	joined := false
	result := make(chan error, 1)
	go func() { result <- ScopeAcquireInheritanceSources(scope, nil) }()
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
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: oldRef}]
	queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == scope.LockTicket && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[holder.LockTicket].Mode&lock.AccessExclusive != 0
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
	if err = e.StageQueryTableRows(context.Background(), replacementStage, replacement, [][]sqltype.Value{{inheritanceQueryInt(e, 9)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, replacementStage)
	if replacement.Address() == c.Address() || e.Storage.Relations[replacement.Address()] == oldStorage || e.Storage.Relations[c.Address()] != nil {
		t.Fatal("replacement did not change physical identity and storage")
	}
	if children := catalog.InheritanceChildren(e.CatalogRead(), p.Address()); len(children) != 1 || children[0] != replacement {
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
	if !errors.Is(err, ids.ErrCatalogReplaced) {
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
	if err = fresh.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	fresh.QuerySources = []exec.Leaf{parent}
	if err = e.Locks.Acquire(freshOp.Context(), fresh.LockTicket, lock.Resource{ObjectRef: parentRef}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	if err = ScopeAcquireInheritanceSources(fresh, nil); err != nil {
		t.Fatal(err)
	}
	collectAuth, err := OperationAuthorization(freshOp)
	if err != nil {
		t.Fatal(err)
	}
	collectAuth.Recapture(InheritanceScopeCatalog(&fresh.Inheritance))
	e.Mu.Lock()
	refs, err := e.QueryTableRefsLocked(collectAuth, collectAuth.State.CurrentUser, fresh.QuerySources, nil, freshOp.Session.SessionToken, plan.RowMark{})
	var view *visibility.View
	if err == nil {
		view, err = fresh.AcquireDataViewsLocked(e, refs)
	}
	var source *exec.Source
	if err == nil {
		source, err = table.CaptureInheritanceQueryPrefix(InheritanceScopeCatalog(&fresh.Inheritance), view, parent, freshOp.Session.SessionToken)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = exec.SelectQuerySources(freshOp.Context(), source); err != nil {
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
	if len(got) != 2 || got[9] != 1 || got[10] != 1 {
		t.Fatal("fresh admitted scan did not use new sentinel identity", got)
	}
	source.Close()
	fresh.Finish(nil)
	lockEmpty(t, e)
}
