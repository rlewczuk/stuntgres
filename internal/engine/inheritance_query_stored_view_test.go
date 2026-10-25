package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Use the incumbent table publication and frozen-view builders, not a public
// SQL witness or a substitute lock manager. The peer probes actual grant rights.
func TestInheritanceQueryStoredViewAdmission(t *testing.T) {
	for _, tc := range []struct {
		name                                                string
		child, only, local, direct, overlapping, weak, wait bool
		mark                                                lock.Mode
	}{
		{name: "ordinary"},
		{name: "only_plain", only: true},
		{name: "only_child", child: true, only: true},
		{name: "default_child", child: true},
		{name: "marked_child", child: true, mark: lock.ForUpdate},
		{name: "marked_only", child: true, only: true, mark: lock.ForUpdate},
		{name: "local_mark", child: true, local: true},
		{name: "direct_overlap", child: true, direct: true, mark: lock.ForUpdate},
		{name: "view_overlap", child: true, overlapping: true},
		{name: "weaker_base", child: true, weak: true, mark: lock.ForUpdate},
		{name: "waited_base", child: true, wait: true, mark: lock.ForUpdate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			stage, parent := queryCreateStage(t, e, "p", 1)
			if err := e.StageQueryTableRows(context.Background(), stage, parent, [][]sqltype.Value{{inheritanceQueryInt(e, 10)}}, ids.TxRef{}); err != nil {
				t.Fatal(err)
			}
			queryCreatePublish(t, e, stage)
			var child *catalog.Relation
			if tc.child {
				predecessor, before := parent, e.CatalogRead()
				storage := e.Storage.Relations[parent.Address()]
				childStage, d := queryCreateStage(t, e, "c", 1)
				d.Inheritance = &catalog.TableInheritance{Parents: inheritanceUnitParents(parent.Address()), Columns: map[int32]catalog.InheritedColumn{d.Attributes[0]: {Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: parent.Attributes[0]}}}}, Checks: map[string]catalog.InheritedConstraint{}, NotNull: map[int32]catalog.InheritedConstraint{}}
				if err := e.StageQueryTableRows(context.Background(), childStage, d, [][]sqltype.Value{{inheritanceQueryInt(e, 20)}}, ids.TxRef{}); err != nil {
					t.Fatal(err)
				}
				queryCreatePublish(t, e, childStage)
				child = d
				parent = e.CatalogRead().MustRelation(predecessor.Address())
				if parent == predecessor || !parent.History.HasSubclasses || predecessor.History.HasSubclasses || parent.Address() != predecessor.Address() || parent.RowType != predecessor.RowType || parent.Revision() == predecessor.Revision() || before.Get(predecessor.Address()) != predecessor || e.Storage.Relations[parent.Address()] != storage {
					t.Fatal("successful child publication did not preserve immutable parent identity/storage and transition subclass history")
				}
			}
			ref := func(d *catalog.Relation) ids.ObjectRef {
				return e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())
			}
			parentRef, parentStorage := ref(parent), e.Storage.Relations[parent.Address()]
			ctx, cancelScan := context.WithCancel(context.Background())
			defer cancelScan()
			op, done, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			addView := func(name string, only, local bool) (*catalog.Relation, ids.ObjectRef) {
				t.Helper()
				query := "SELECT i FROM p"
				if only {
					query = "SELECT i FROM ONLY p"
				}
				if local {
					query += " FOR UPDATE"
				}
				// CREATE VIEW analysis records the frozen column-frame and demand
				// presentation metadata; standalone SELECT analysis does not.
				raw := statement(t, "CREATE VIEW "+name+" AS "+query).(*parser.CreateView)
				e.Mu.Lock()
				auth, bindErr := e.AuthorizationLocked(op)
				var b analyze.Binding
				if bindErr == nil {
					b, bindErr = Describe(op.Context(), e.AnalysisHost(nil), raw, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil)
				}
				var definition *stored.View
				if bindErr == nil {
					definition, bindErr = view.FreezeDefinition(DDLHost{E: e}, auth.Catalog, CatalogSourceTemporaryOwner(auth), raw.Query, b, false, false, 0, catalog.ViewRelation, false)
				}
				var oid ids.OID
				if bindErr == nil {
					oid, bindErr = e.Catalog.AllocateOIDLocked()
				}
				if bindErr != nil {
					e.Mu.Unlock()
					t.Fatal(bindErr)
				}
				d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: parent.Namespace(), ObjectName: name, ObjectRevision: 1, Owner: parent.Owner, RelationKind: catalog.ViewRelation, View: stored.ViewPayload(definition), Columns: append([]catalog.Column(nil), b.Query.Root.Schema()...), Index: map[string]int{"i": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
				d.Columns[0].OriginTable, d.Columns[0].OriginAttribute = uint32(oid), 1
				viewStage := e.BeginCatalogStage(auth.Catalog)
				bindErr = e.StageRowTypeLocked(viewStage, d)
				if bindErr == nil {
					bindErr = viewStage.Create(d)
				}
				e.Mu.Unlock()
				if bindErr != nil {
					t.Fatal(bindErr)
				}
				queryCreatePublish(t, e, viewStage)
				return d, ref(d)
			}
			view, viewRef := addView("v", tc.only, tc.local)
			var otherView *catalog.Relation
			var otherRef ids.ObjectRef
			if tc.overlapping {
				otherView, otherRef = addView("marked_v", false, true)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			if err = scope.Gate(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			scope.LockedRef = viewRef
			scope.QuerySources = []exec.Leaf{{Ref: viewRef, Relation: view, Mark: plan.RowMark{Mode: tc.mark}}}
			mode := lock.AccessShare
			if tc.mark != 0 {
				mode = lock.RowShare
			}
			if err = e.Locks.Acquire(op.Context(), scope.LockTicket, lock.Resource{ObjectRef: viewRef}, mode); err != nil {
				t.Fatal(err)
			}
			if tc.direct {
				scope.QuerySources = append(scope.QuerySources, exec.Leaf{Ref: parentRef, Relation: parent, Storage: parentStorage})
				if err = e.Locks.Acquire(op.Context(), scope.LockTicket, lock.Resource{ObjectRef: parentRef}, lock.AccessShare); err != nil {
					t.Fatal(err)
				}
			}
			if tc.overlapping {
				scope.QuerySources = append(scope.QuerySources, exec.Leaf{Ref: otherRef, Relation: otherView})
				if err = e.Locks.Acquire(op.Context(), scope.LockTicket, lock.Resource{ObjectRef: otherRef}, lock.AccessShare); err != nil {
					t.Fatal(err)
				}
			}
			if tc.weak {
				if err = ScopeAcquireBoundViewRelationModes(scope, []ids.ObjectRef{parentRef}, nil); err != nil {
					t.Fatal(err)
				}
				err = ScopeAcquireInheritanceSources(scope, map[ids.ObjectRef]lock.Mode{parentRef: 0})
			} else if tc.wait {
				holderOp, holderDone, holdErr := testOperation(e, context.Background())
				if holdErr != nil {
					t.Fatal(holdErr)
				}
				defer holderDone()
				holder, holdErr := e.StartStatement(holderOp)
				if holdErr != nil {
					t.Fatal(holdErr)
				}
				defer holder.Finish(nil)
				if holdErr = holder.Gate(&parser.Select{}); holdErr != nil {
					t.Fatal(holdErr)
				}
				if holdErr = e.Locks.Acquire(holderOp.Context(), holder.LockTicket, lock.Resource{ObjectRef: parentRef}, lock.AccessExclusive); holdErr != nil {
					t.Fatal(holdErr)
				}
				result := make(chan error, 1)
				joined := false
				go func() { _, err := scope.AcquireViewSources(false); result <- err }()
				defer func() {
					cancelScan()
					holder.Finish(nil)
					if !joined {
						select {
						case <-result:
						case <-time.After(5 * time.Second):
							t.Error("stored-base admission worker did not join")
						}
					}
				}()
				lockPending(t, e, 1)
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: parentRef}]
				queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == scope.LockTicket && entry.Pending[0].Mode == lock.RowShare
				if !queued {
					t.Fatal("stored view did not wait on exact marked-base identity")
				}
				if holdErr = holder.Finish(nil); holdErr != nil {
					t.Fatal(holdErr)
				}
				select {
				case err = <-result:
					joined = true
				case <-time.After(5 * time.Second):
					t.Fatal("stored-base admission did not resume")
				}
			} else {
				_, err = scope.AcquireViewSources(false)
			}
			if err != nil {
				t.Fatal(err)
			}
			marked := tc.mark != 0 || tc.local || tc.overlapping
			wantBase := lock.AccessShare
			if marked {
				wantBase = lock.RowShare
				if tc.direct || tc.weak {
					wantBase |= lock.AccessShare
				}
			}
			wantChild := lock.Mode(0)
			if tc.child && !tc.only {
				wantChild = lock.AccessShare
				if marked {
					wantChild = lock.RowShare
				}
			}
			var childRef ids.ObjectRef
			if child != nil {
				childRef = ref(child)
			}
			for _, want := range []struct {
				ref  ids.ObjectRef
				mode lock.Mode
			}{{parentRef, wantBase}, {childRef, wantChild}} {
				if want.ref == (ids.ObjectRef{}) {
					continue
				}
				lockState := e.Locks.Inspect()
				var got lock.Mode
				if entry := lockState.Resources[lock.Resource{ObjectRef: want.ref}]; entry != nil {
					got = entry.Holders[scope.LockTicket].Mode
				}
				if got != want.mode {
					t.Fatalf("physical identity %v grant %v want %v", want.ref, got, want.mode)
				}
			}
			if scope.QuerySources[0].Ref != viewRef || scope.QuerySources[0].Relation != view {
				t.Fatal("physical expansion replaced logical view authority")
			}
			for _, physical := range InheritanceScopeRelations(&scope.Inheritance) {
				if !tc.weak && physical.Ref == parentRef {
					t.Fatal("already admitted base gained duplicate physical manifest ownership")
				}
			}
			collectAuth, err := OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			catalog := ScopeInheritanceRead(scope, e.PrivateCatalogLocked(scope.Owner.Catalog))
			collectAuth.Recapture(catalog)
			refs, captureErr := e.QueryTableRefsLocked(collectAuth, collectAuth.State.CurrentUser, scope.QuerySources, nil, op.Session.SessionToken, plan.RowMark{})
			var snapshot *visibility.View
			if captureErr == nil {
				snapshot, captureErr = scope.AcquireDataViewsLocked(e, refs)
			}
			var source *exec.Source
			if captureErr == nil {
				source, captureErr = table.CaptureInheritanceQueryPrefix(catalog, snapshot, exec.Leaf{Ref: parentRef, Relation: parent, Storage: parentStorage, Only: stored.ViewOf(view).Sources()[0].Only}, op.Session.SessionToken)
			}
			validBase := catalog.Get(parent.Address()) == parent && e.PrivateStorageLocked(scope.Owner.Catalog, parent.Address()) == parentStorage
			e.Mu.Unlock()
			if captureErr != nil {
				t.Fatal(captureErr)
			}
			defer source.Close()
			if !validBase {
				t.Fatal("stored base identity/storage changed during admission")
			}
			if len(parent.Columns) != 1 || parent.Columns[0].Name != "i" || parent.Columns[0].Usage.Type != testUsage(e, sqltype.Int4).Type {
				t.Fatal("parent metadata changed")
			}
			if err = exec.SelectQuerySources(op.Context(), source); err != nil {
				t.Fatal(err)
			}
			bag := map[int64]int{}
			for i := range source.Count() {
				value, valueErr := source.Row(i)[0].Integer()
				if valueErr != nil {
					t.Fatal(valueErr)
				}
				bag[value]++
			}
			want20 := 0
			if tc.child && !tc.only {
				want20 = 1
			}
			if bag[10] != 1 || bag[20] != want20 || len(bag) != 1+want20 {
				t.Fatal("frozen source physical bag", bag)
			}
			source.Close()
			peerCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			peerOp, peerDone, err := testOperation(e, peerCtx)
			if err != nil {
				t.Fatal(err)
			}
			defer peerDone()
			peer, err := e.StartStatement(peerOp)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Finish(nil)
			if err = peer.Gate(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			request, err := e.Locks.StartAcquire(peerCtx, peer.LockTicket, lock.Resource{ObjectRef: parentRef}, lock.Exclusive, lock.Policy{TimeoutSlot: &peerOp.LockWait})
			if err != nil {
				t.Fatal(err)
			}
			if marked {
				if request == nil {
					t.Fatal("peer Exclusive bypassed effective RowShare")
				}
				lockPending(t, e, 1)
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: parentRef}]
				queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == peer.LockTicket && entry.Pending[0].Mode == lock.Exclusive
				if !queued {
					t.Fatal("peer did not queue on exact stored-base identity")
				}
			} else if request != nil {
				t.Fatal("peer Exclusive incorrectly conflicts with AccessShare-only base")
			}
			if err = scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if request != nil {
				if err = request.Wait(); err != nil {
					t.Fatal("peer did not resume after source cleanup", err)
				}
			}
			if err = peer.Finish(nil); err != nil {
				t.Fatal(err)
			}
			lockEmpty(t, e)
		})
	}
}
