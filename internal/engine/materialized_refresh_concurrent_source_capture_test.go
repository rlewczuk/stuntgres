package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"testing"
	"time"
)

func concurrentSourceOuter(t *testing.T, e *Engine) *catalog.Relation {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	statements, err := parser.Parse("CREATE MATERIALIZED VIEW source_outer AS SELECT a.i FROM unit_mv a JOIN unit_mv b ON a.i=b.i CROSS JOIN source_gate g WITH NO DATA")
	if err != nil {
		t.Fatal(err)
	}
	stmt := statements[0].(*parser.CreateQueryTable)
	input, _ := sessionPresentation(op.session)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	bound, err := e.describe(context.Background(), stmt, auth, ids.ObjectRef{}, input, op, nil, nil)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	stage, d, skipped, err := e.prepareQueryTableLocked(op, auth, stmt, bound)
	e.mu.Unlock()
	if err != nil || skipped {
		t.Fatal("source outer stage", skipped, err)
	}
	d.Populated = true
	if err := e.stageQueryTableRows(context.Background(), stage, d, refreshUnitRows(t, d, 3), ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return d
}

func TestMaterializedRefreshConcurrentRetainedSourceCapture(t *testing.T) {
	e := New()
	inner := refreshUnitRelation(t, e)
	materializedConcurrentUnitIndex(t, e, inner, "inner_u", "i", "")
	inner = e.catalog.state.MustRelation(inner.Address())
	gateStage, gate := queryCreateStage(t, e, "source_gate", 1)
	queryCreatePublish(t, e, gateStage)
	outer := concurrentSourceOuter(t, e)
	index := materializedConcurrentUnitIndex(t, e, outer, "outer_u", "i", "")
	outer = e.catalog.state.MustRelation(outer.Address())
	innerStorage, gateStorage := e.storage.Relations[inner.Address()], e.storage.Relations[gate.Address()]
	// A real end marker must affect visibility, rather than merely exercising
	// preservation of a non-nil but empty root.
	deleted := innerStorage.Versions[2]
	innerStorage.Ends = &deletionRoot{Rows: map[ids.RowID]rowEnd{
		deleted.Row: {Version: deleted.Version, End: e.commitSeq},
	}}
	frontier := e.commitSeq
	innerVersions, gateVersions := innerStorage.Versions, gateStorage.Versions
	innerEnds, gateEnds := innerStorage.Ends, gateStorage.Ends
	holder := sessionStart(t, e, nil)
	holderTx := transactionBegin(t, holder)
	gateRef := e.catalog.Domain.Ref(e.catalog.state.Epoch(), gate.Address())
	defer e.endLockOwner(holder.transactions.ticket)
	if err := e.locks.Acquire(context.Background(), holder.transactions.ticket, lock.Resource{ObjectRef: gateRef}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scope := refreshUnitScope(t, e, ctx)
	stmt := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: outer.Name()}}}, Concurrent: parser.Position{Character: 27}}
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		e.endLockOwner(holder.transactions.ticket)
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("source acquisition worker did not join")
			}
		}
	}()
	// This calls the actual target admission and acquireViewSources, not a
	// manually assembled visibilityData or materializedSourceFence.
	go func() { done <- scope.acquireMaterializedRefresh(stmt) }()
	lockPending(t, e, 1)
	assertSourceWait := func() {
		t.Helper()
		lockState := e.locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: gateRef}]
		waiting := entry != nil && len(entry.Pending) == 1 &&
			entry.Pending[0].Ticket == scope.ticket && entry.Pending[0].Mode == lock.AccessShare &&
			entry.Holders[holder.transactions.ticket].Mode&lock.AccessExclusive != 0
		innerRef := e.catalog.Domain.Ref(gateRef.Epoch, inner.Address())
		innerEntry := lockState.Resources[lock.Resource{ObjectRef: innerRef}]
		granted := innerEntry != nil && innerEntry.Holders[scope.ticket].Mode&lock.AccessShare != 0
		if !waiting || !granted {
			t.Fatal("not the actual gate AS wait with inner AS granted")
		}
	}
	assertSourceWait()
	// The pending-request mutex observation synchronizes with the prewait
	// manifest capture. The holder keeps the worker blocked during inspection.
	snapshot := scope.materializedSourceView
	assertManifest := func() {
		t.Helper()
		if scope.materializedSourceView != snapshot || snapshot == nil || snapshot.frontier != frontier || len(snapshot.committed) != 2 {
			t.Fatal("physical mixed producer manifest changed", snapshot)
		}
		for address, storage := range map[ids.ObjectAddress]*relationStorage{inner.Address(): innerStorage, gate.Address(): gateStorage} {
			versions, ends := innerVersions, innerEnds
			if address == gate.Address() {
				versions, ends = gateVersions, gateEnds
			}
			if snapshot.generationAt(address) != storage || !sameVersionRoot(snapshot.committed[address], versions) ||
				snapshot.ends[address] != ends || snapshot.frontierAt(address) != frontier {
				t.Fatal("physical source did not retain captured storage/versions/ends/frontier", address)
			}
		}
		if innerEnds == nil || len(innerEnds.Rows) != 1 || liveVersion(deleted, frontier, snapshot.ends[inner.Address()], nil) ||
			!liveVersion(innerVersions[0], frontier, snapshot.ends[inner.Address()], nil) {
			t.Fatal("captured source end root did not distinguish deleted and live versions")
		}
		aliases := 0
		for _, leaf := range viewOf(outer).sources {
			view, err := scope.AcquireDataView(leaf.ref)
			if err != nil || view.data.Load() != snapshot {
				t.Fatal("source/alias recaptured separately", leaf.ref, err)
			}
			if leaf.ref.Address == inner.Address() {
				aliases++
			}
		}
		if aliases != 2 {
			t.Fatal("fixture lost repeated physical MV aliases", aliases)
		}
	}
	assertManifest()
	inner = concurrentUnitReplace(t, e, inner, refreshUnitRows(t, inner, 1), true, true)
	if e.storage.Relations[inner.Address()] == innerStorage || e.commitSeq <= frontier {
		t.Fatal("peer did not publish a new committed source incarnation")
	}
	assertSourceWait()
	assertManifest()
	e.endLockOwner(holder.transactions.ticket)
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal("source grant validation", err)
		}
	case <-ctx.Done():
		t.Fatal("source acquisition join", ctx.Err())
	}
	assertManifest()
	transactionEnd(t, holder, holderTx, false)
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	stage.querySources = scope.materializedSources
	stage.materializedSourceView = snapshot
	provider := stage.Base.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(outer.RowType)})
	if provider == nil {
		t.Fatal("missing target rowtype provider")
	}
	stage.domainBases = []catalog.Descriptor{provider}
	if err := e.stageMaterializedRows(context.Background(), stage, outer, e.storage.Relations[outer.Address()], refreshUnitRows(t, outer, 3), true, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	stage.materializedReplacement.concurrent = true
	changes := newCatalogChanges()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.captureCatalogStageLocked(stage, changes, e.catalog.state, nil); err != nil {
		t.Fatal("actual retained capture", err)
	}
	if len(changes.materializedSources) != 1 {
		t.Fatal("dedicated source fence not generated", changes.materializedSources)
	}
	fence := changes.materializedSources[0]
	if fence.ref.Address != inner.Address() || fence.relation != snapshot.catalog.Get(inner.Address()) || fence.storage != innerStorage {
		t.Fatal("generated source fence retargeted", fence)
	}
	if _, strict := changes.originals[inner.Address()]; strict {
		t.Fatal("source-only observation became strict predecessor")
	}
	for _, d := range []catalog.Descriptor{outer, index, gate, provider} {
		if changes.originals[d.Address()] != d {
			t.Fatal("strict target/index/ordinary/provider original lost", d.Address())
		}
	}
	clone := cloneCatalogChanges(changes)
	if err := e.catalogPreflightLocked(clone); err != nil {
		t.Fatal("generated fence rejected compatible source", err)
	}
	// Even when the same MV has a source fence, an actual provider observation
	// must retain strict predecessor authority; compatibility cannot replace it.
	stage.domainBases = []catalog.Descriptor{provider, inner}
	if err := e.captureCatalogStageLocked(stage, changes, e.catalog.state, nil); err != nil {
		t.Fatal(err)
	}
	if changes.originals[inner.Address()] != inner {
		t.Fatal("provider/source overlap lost strict original")
	}
	for _, d := range []catalog.Descriptor{outer, index, provider, inner} {
		original := e.catalog.state.Get(d.Address())
		e.catalog.state.RemoveDescriptor(d.Address())
		err := e.catalogPreflightLocked(changes)
		e.catalog.state.SetDescriptor(d.Address(), original)
		if err == nil {
			t.Fatal("source compatibility weakened strict original", d.Address())
		}
	}
}
