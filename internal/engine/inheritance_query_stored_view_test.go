package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

// Use the incumbent table publication and frozen-view builders, not a public
// SQL witness or a substitute lock manager. The peer probes actual grant rights.
func TestInheritanceQueryStoredViewAdmission(t *testing.T) {
	for _, tc := range []struct {
		name                                                string
		child, only, local, direct, overlapping, weak, wait bool
		mark                                                relationLockMode
	}{
		{name: "ordinary"},
		{name: "only_plain", only: true},
		{name: "only_child", child: true, only: true},
		{name: "default_child", child: true},
		{name: "marked_child", child: true, mark: rowUpdate},
		{name: "marked_only", child: true, only: true, mark: rowUpdate},
		{name: "local_mark", child: true, local: true},
		{name: "direct_overlap", child: true, direct: true, mark: rowUpdate},
		{name: "view_overlap", child: true, overlapping: true},
		{name: "weaker_base", child: true, weak: true, mark: rowUpdate},
		{name: "waited_base", child: true, wait: true, mark: rowUpdate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			stage, parent := queryCreateStage(t, e, "p", 1)
			if err := e.stageQueryTableRows(context.Background(), stage, parent, [][]sqltype.Value{{inheritanceQueryInt(e, 10)}}, TxRef{}); err != nil {
				t.Fatal(err)
			}
			queryCreatePublish(t, e, stage)
			var child *relationDescriptor
			if tc.child {
				predecessor, before := parent, e.CatalogRead()
				storage := e.storage[parent.address]
				childStage, d := queryCreateStage(t, e, "c", 1)
				d.inheritance = &tableInheritance{parents: []ids.ObjectAddress{parent.address}, columns: map[int32]inheritedColumn{d.attributes[0]: {origins: []inheritanceOrigin{{parent: parent.address, attribute: parent.attributes[0]}}}}, checks: map[string]inheritedConstraint{}, notNull: map[int32]inheritedConstraint{}}
				if err := e.stageQueryTableRows(context.Background(), childStage, d, [][]sqltype.Value{{inheritanceQueryInt(e, 20)}}, TxRef{}); err != nil {
					t.Fatal(err)
				}
				queryCreatePublish(t, e, childStage)
				child = d
				parent = e.CatalogRead().state.descriptors[predecessor.address].(*relationDescriptor)
				if parent == predecessor || !parent.hasSubclasses || predecessor.hasSubclasses || parent.address != predecessor.address || parent.rowType != predecessor.rowType || parent.revision == predecessor.revision || before.state.descriptors[predecessor.address] != predecessor || e.storage[parent.address] != storage {
					t.Fatal("successful child publication did not preserve immutable parent identity/storage and transition subclass history")
				}
			}
			ref := func(d *relationDescriptor) ids.ObjectRef {
				return e.domain.Ref(e.CatalogRead().Epoch(), d.address)
			}
			parentRef, parentStorage := ref(parent), e.storage[parent.address]
			ctx, cancelScan := context.WithCancel(context.Background())
			defer cancelScan()
			op, done, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			addView := func(name string, only, local bool) (*relationDescriptor, ids.ObjectRef) {
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
				e.mu.Lock()
				auth, bindErr := e.authorizationLocked(op)
				var b binding
				if bindErr == nil {
					b, bindErr = e.describe(op.Context(), raw, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil, nil)
				}
				var definition *viewDefinition
				if bindErr == nil {
					definition, bindErr = e.freezeViewDefinitionLocked(auth, raw.Query, b, false, false, 0, viewRelation, false)
				}
				var oid ids.OID
				if bindErr == nil {
					oid, bindErr = e.allocateOIDLocked()
				}
				if bindErr != nil {
					e.mu.Unlock()
					t.Fatal(bindErr)
				}
				d := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, namespace: parent.namespace, name: name, revision: 1, owner: parent.owner, relationKind: viewRelation, view: definition, columns: append([]ColumnDescriptor(nil), b.query.Root.Schema()...), index: map[string]int{"i": 0}, attributes: []int32{1}, attributeHighWater: 1}
				d.columns[0].OriginTable, d.columns[0].OriginAttribute = uint32(oid), 1
				viewStage := e.BeginCatalogStage(auth.catalog)
				bindErr = e.stageRowTypeLocked(viewStage, d)
				if bindErr == nil {
					bindErr = viewStage.Create(d)
				}
				e.mu.Unlock()
				if bindErr != nil {
					t.Fatal(bindErr)
				}
				queryCreatePublish(t, e, viewStage)
				return d, ref(d)
			}
			view, viewRef := addView("v", tc.only, tc.local)
			var otherView *relationDescriptor
			var otherRef ids.ObjectRef
			if tc.overlapping {
				otherView, otherRef = addView("marked_v", false, true)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			if err = scope.gate(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			scope.lockedRef = viewRef
			scope.querySources = []queryLeaf{{ref: viewRef, relation: view, mark: queryRowMark{mode: tc.mark}}}
			mode := accessShare
			if tc.mark != 0 {
				mode = relationRowShare
			}
			if err = e.locks.acquire(op.Context(), scope.ticket, lockResource{ObjectRef: viewRef}, mode); err != nil {
				t.Fatal(err)
			}
			if tc.direct {
				scope.querySources = append(scope.querySources, queryLeaf{ref: parentRef, relation: parent, storage: parentStorage})
				if err = e.locks.acquire(op.Context(), scope.ticket, lockResource{ObjectRef: parentRef}, accessShare); err != nil {
					t.Fatal(err)
				}
			}
			if tc.overlapping {
				scope.querySources = append(scope.querySources, queryLeaf{ref: otherRef, relation: otherView})
				if err = e.locks.acquire(op.Context(), scope.ticket, lockResource{ObjectRef: otherRef}, accessShare); err != nil {
					t.Fatal(err)
				}
			}
			if tc.weak {
				if err = scope.acquireBoundViewRelationModes([]ids.ObjectRef{parentRef}, nil); err != nil {
					t.Fatal(err)
				}
				err = scope.acquireInheritanceSources(map[ids.ObjectRef]relationLockMode{parentRef: 0})
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
				if holdErr = holder.gate(&parser.Select{}); holdErr != nil {
					t.Fatal(holdErr)
				}
				if holdErr = e.locks.acquire(holderOp.Context(), holder.ticket, lockResource{ObjectRef: parentRef}, accessExclusive); holdErr != nil {
					t.Fatal(holdErr)
				}
				result := make(chan error, 1)
				joined := false
				go func() { result <- scope.acquireViewSources() }()
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
				e.locks.mu.Lock()
				entry := e.locks.entries[lockResource{ObjectRef: parentRef}]
				queued := entry != nil && len(entry.pending) == 1 && entry.pending[0].ticket == scope.ticket && entry.pending[0].mode == relationRowShare
				e.locks.mu.Unlock()
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
				err = scope.acquireViewSources()
			}
			if err != nil {
				t.Fatal(err)
			}
			marked := tc.mark != 0 || tc.local || tc.overlapping
			wantBase := accessShare
			if marked {
				wantBase = relationRowShare
				if tc.direct || tc.weak {
					wantBase |= accessShare
				}
			}
			wantChild := relationLockMode(0)
			if tc.child && !tc.only {
				wantChild = accessShare
				if marked {
					wantChild = relationRowShare
				}
			}
			var childRef ids.ObjectRef
			if child != nil {
				childRef = ref(child)
			}
			for _, want := range []struct {
				ref  ids.ObjectRef
				mode relationLockMode
			}{{parentRef, wantBase}, {childRef, wantChild}} {
				if want.ref == (ids.ObjectRef{}) {
					continue
				}
				e.locks.mu.Lock()
				var got relationLockMode
				if entry := e.locks.entries[lockResource{ObjectRef: want.ref}]; entry != nil {
					got = entry.holders[scope.ticket].mode
				}
				e.locks.mu.Unlock()
				if got != want.mode {
					t.Fatalf("physical identity %v grant %v want %v", want.ref, got, want.mode)
				}
			}
			if scope.querySources[0].ref != viewRef || scope.querySources[0].relation != view {
				t.Fatal("physical expansion replaced logical view authority")
			}
			for _, physical := range scope.inheritanceRelations {
				if !tc.weak && physical.ref == parentRef {
					t.Fatal("already admitted base gained duplicate physical manifest ownership")
				}
			}
			collectAuth, err := op.Authorization()
			if err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			catalog := scope.inheritanceRead(e.privateCatalogLocked(scope.owner.catalog))
			collectAuth.recaptureCatalog(catalog)
			refs, captureErr := e.queryTableRefsLocked(collectAuth, collectAuth.state.CurrentUser, scope.querySources, nil, op.session.token, queryRowMark{})
			var snapshot *VisibilityView
			if captureErr == nil {
				snapshot, captureErr = scope.acquireDataViewsLocked(e, refs)
			}
			var source *querySource
			if captureErr == nil {
				source, captureErr = captureInheritanceQueryPrefix(catalog, snapshot, queryLeaf{ref: parentRef, relation: parent, storage: parentStorage, only: view.view.sources[0].only}, op.session.token)
			}
			validBase := catalog.state.descriptors[parent.address] == parent && e.privateStorageLocked(scope.owner.catalog, parent.address) == parentStorage
			e.mu.Unlock()
			if captureErr != nil {
				t.Fatal(captureErr)
			}
			defer source.close()
			if !validBase {
				t.Fatal("stored base identity/storage changed during admission")
			}
			if len(parent.columns) != 1 || parent.columns[0].Name != "i" || parent.columns[0].Usage.Type != testUsage(e, sqltype.Int4).Type {
				t.Fatal("parent metadata changed")
			}
			if err = selectQuerySources(op.Context(), source); err != nil {
				t.Fatal(err)
			}
			bag := map[int64]int{}
			for i := range source.count() {
				value, valueErr := source.row(i)[0].Integer()
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
			source.close()
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
			if err = peer.gate(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			request, err := e.locks.startAcquire(peerCtx, peer.ticket, lockResource{ObjectRef: parentRef}, relationExclusive, relationLockPolicy{operation: peerOp})
			if err != nil {
				t.Fatal(err)
			}
			if marked {
				if request == nil {
					t.Fatal("peer Exclusive bypassed effective RowShare")
				}
				lockPending(t, e, 1)
				e.locks.mu.Lock()
				entry := e.locks.entries[lockResource{ObjectRef: parentRef}]
				queued := entry != nil && len(entry.pending) == 1 && entry.pending[0].ticket == peer.ticket && entry.pending[0].mode == relationExclusive
				e.locks.mu.Unlock()
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
				if err = request.wait(); err != nil {
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
