package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"testing"
	"time"
)

func TestMaterializedRefreshConcurrentLocks(t *testing.T) {
	for _, tc := range []struct {
		name            string
		target          bool
		held            lock.Mode
		blocked, noData bool
	}{
		{"reader_target", true, lock.AccessShare, false, false},
		{"refresh_target", true, lock.Exclusive, true, false},
		{"plain_target", true, lock.AccessExclusive, true, false},
		{"source_exclusive", false, lock.AccessExclusive, true, false},
		{"source_writer", false, lock.RowExclusive, false, false},
		{"invalid_no_data_before_source", false, lock.AccessExclusive, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.catalog.state.MustRelation(d.Address())
			stage, source := queryCreateStage(t, e, "concurrent_unit_source", 1)
			queryCreatePublish(t, e, stage)
			sourceRef := e.catalog.Domain.Ref(e.catalog.state.Epoch(), source.Address())
			definition := *viewOf(d)
			definition.sources = []viewLeaf{{ref: sourceRef, attributes: source.Attributes, reads: source.Attributes}}
			d.View = &definition
			targetRef := e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address())
			holder := sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			heldRef := sourceRef
			if tc.target {
				heldRef = targetRef
			}
			if err := e.locks.Acquire(context.Background(), holder.transactions.ticket, lock.Resource{ObjectRef: heldRef}, tc.held); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			scope := refreshUnitScope(t, e, ctx)
			stmt := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}, Concurrent: parser.Position{Character: 27}, NoData: tc.noData}
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
					entry := lockState.Resources[lock.Resource{ObjectRef: targetRef}]
					granted := entry != nil && entry.Holders[scope.ticket].Mode&lock.Exclusive != 0
					if !granted {
						t.Fatal("source waiter did not retain target EXCLUSIVE")
					}
					reader := refreshUnitScope(t, e, ctx)
					request, err := e.locks.StartAcquire(ctx, reader.ticket, lock.Resource{ObjectRef: targetRef}, lock.AccessShare, lock.Policy{})
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
				if len(scope.materializedOrZero().sources) != 0 {
					t.Fatal("option conflict acquired sources")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: targetRef}]
				granted := entry != nil && entry.Holders[scope.ticket].Mode&lock.Exclusive != 0
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
	d = e.catalog.state.MustRelation(d.Address())
	old := e.storage.Relations[d.Address()]
	frontier := e.visibility.CommitSeq()
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := e.stageMaterializedRows(context.Background(), stage, d, old, refreshUnitRows(t, d, 128), true, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	stage.materialized().replacement.concurrent = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counting := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 32}
	// Cancel inside the actual index candidate builder, before any install owner.
	entries, err := buildIndexEntries(&exec.Context{EvalStatement: exec.EvalStatement{Context: counting}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, stage.materializedOrZero().replacement.descriptor, index.SQLIndex, stage.materializedOrZero().replacement.storage, nil)
	if entries != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("index preparation ignored cancellation", err)
	}
	if e.storage.Relations[d.Address()] != old || e.visibility.CommitSeq() != frontier || old.MaterializedLineage != nil || stage.materializedOrZero().replacement.storage.MaterializedLineage != nil {
		t.Fatal("canceled index preparation published or stamped")
	}
}
