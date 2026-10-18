package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
	"time"
)

func TestMaterializedRefreshConcurrentLocks(t *testing.T) {
	for _, tc := range []struct {
		name            string
		target          bool
		held            relationLockMode
		blocked, noData bool
	}{
		{"reader_target", true, accessShare, false, false},
		{"refresh_target", true, relationExclusive, true, false},
		{"plain_target", true, accessExclusive, true, false},
		{"source_exclusive", false, accessExclusive, true, false},
		{"source_writer", false, rowExclusive, false, false},
		{"invalid_no_data_before_source", false, accessExclusive, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.catalog.state.descriptors[d.address].(*relationDescriptor)
			stage, source := queryCreateStage(t, e, "concurrent_unit_source", 1)
			queryCreatePublish(t, e, stage)
			sourceRef := e.catalog.domain.Ref(e.catalog.state.epoch, source.address)
			definition := *d.view
			definition.sources = []viewLeaf{{ref: sourceRef, attributes: source.attributes, reads: source.attributes}}
			d.view = &definition
			targetRef := e.catalog.domain.Ref(e.catalog.state.epoch, d.address)
			holder := sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			heldRef := sourceRef
			if tc.target {
				heldRef = targetRef
			}
			if err := e.locks.Acquire(context.Background(), holder.transactions.ticket, lockResource{ObjectRef: heldRef}, tc.held); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			scope := refreshUnitScope(t, e, ctx)
			stmt := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.name}}}, Concurrent: parser.Position{Character: 27}, NoData: tc.noData}
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				e.endLockOwner(holder.transactions.ticket)
				if !joined {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					select {
					case <-done:
					case <-cleanup.Done():
						t.Error("concurrent lock worker cleanup", cleanup.Err())
					}
				}
			}()
			go func() { done <- scope.acquireMaterializedRefresh(stmt) }()
			if tc.blocked {
				lockPending(t, e, 1)
				if !tc.target {
					lockState := e.locks.Inspect()
					entry := lockState.Resources[lockResource{ObjectRef: targetRef}]
					granted := entry != nil && entry.Holders[scope.ticket].Mode&relationExclusive != 0
					if !granted {
						t.Fatal("source waiter did not retain target EXCLUSIVE")
					}
					reader := refreshUnitScope(t, e, ctx)
					request, err := e.locks.StartAcquire(ctx, reader.ticket, lockResource{ObjectRef: targetRef}, accessShare, relationLockPolicy{})
					if request != nil || err != nil {
						t.Fatal("real SELECT grant blocked behind concurrent source waiter", err)
					}
					if err := reader.Finish(nil); err != nil {
						t.Fatal(err)
					}
				}
				cancel()
			}
			var err error
			select {
			case err = <-done:
				joined = true
			case <-ctx.Done():
				// A cancellation wakes the real worker asynchronously; join under its own
				// bounded cleanup context instead of selecting the canceled context again.
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				select {
				case err = <-done:
					joined = true
				case <-cleanup.Done():
					t.Fatal("concurrent lock worker join", cleanup.Err())
				}
			}
			if tc.blocked {
				if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
					t.Fatal("lock cancellation", err)
				}
			} else if tc.noData {
				materializedConcurrentUnitError(t, err, "42601", "CONCURRENTLY and WITH NO DATA options cannot be used together")
				if len(scope.materializedSources) != 0 {
					t.Fatal("option conflict acquired sources")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lockResource{ObjectRef: targetRef}]
				granted := entry != nil && entry.Holders[scope.ticket].Mode&relationExclusive != 0
				if !granted {
					t.Fatal("concurrent refresh used wrong target mode")
				}
			}
			scope.Finish(err)
			transactionEnd(t, holder, tx, false)
			lockEmpty(t, e)
		})
	}
}

func TestMaterializedRefreshConcurrentIndexCancellation(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	index := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.catalog.state.descriptors[d.address].(*relationDescriptor)
	old := e.storage[d.address]
	frontier := e.commitSeq
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := e.stageMaterializedRows(context.Background(), stage, d, old, refreshUnitRows(t, d, 128), true, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	stage.materializedReplacement.concurrent = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counting := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 32}
	// Cancel inside the actual index candidate builder, before any install owner.
	entries, err := buildIndexEntries(&EvalContext{Context: counting, Scope: 1, Frame: 1}, stage.materializedReplacement.descriptor, index.sqlIndex, stage.materializedReplacement.storage, nil)
	if entries != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("index preparation ignored cancellation", err)
	}
	if e.storage[d.address] != old || e.commitSeq != frontier || old.materializedLineage != nil || stage.materializedReplacement.storage.materializedLineage != nil {
		t.Fatal("canceled index preparation published or stamped")
	}
}
