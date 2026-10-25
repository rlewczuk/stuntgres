package viewcmds_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
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
			e := engine.New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.Catalog.State.MustRelation(d.Address())
			stage, source := queryCreateStage(t, e, "concurrent_unit_source", 1)
			queryCreatePublish(t, e, stage)
			sourceRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), source.Address())
			d.View = stored.ViewOf(d).With(func(p *stored.ViewParts) {
				p.Sources = []stored.ViewLeaf{{Ref: sourceRef, Attributes: source.Attributes, Reads: source.Attributes}}
			})
			targetRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
			holder := enginetest.StartSession(t, e, nil)
			tx := enginetest.TransactionBegin(t, holder)
			heldRef := sourceRef
			if tc.target {
				heldRef = targetRef
			}
			if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: heldRef}, tc.held); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			scope := refreshUnitScope(t, e, ctx)
			stmt := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}, Concurrent: parser.Position{Character: 27}, NoData: tc.noData}
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				e.EndLockOwner(holder.Transactions.Ticket)
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
			go func() { done <- core.ScopeAcquireMaterializedRefresh(scope, stmt) }()
			if tc.blocked {
				enginetest.LockPending(t, e, 1)
				if !tc.target {
					lockState := e.Locks.Inspect()
					entry := lockState.Resources[lock.Resource{ObjectRef: targetRef}]
					granted := entry != nil && entry.Holders[scope.LockTicket].Mode&lock.Exclusive != 0
					if !granted {
						t.Fatal("source waiter did not retain target EXCLUSIVE")
					}
					reader := refreshUnitScope(t, e, ctx)
					request, err := e.Locks.StartAcquire(ctx, reader.LockTicket, lock.Resource{ObjectRef: targetRef}, lock.AccessShare, lock.Policy{})
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
				if len(scope.MaterializedOrZero().Sources) != 0 {
					t.Fatal("option conflict acquired sources")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: targetRef}]
				granted := entry != nil && entry.Holders[scope.LockTicket].Mode&lock.Exclusive != 0
				if !granted {
					t.Fatal("concurrent refresh used wrong target mode")
				}
			}
			scope.Finish(err)
			enginetest.TransactionEnd(t, holder, tx, false)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestMaterializedRefreshConcurrentIndexCancellation(t *testing.T) {
	e := engine.New()
	d := refreshUnitRelation(t, e)
	idx := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.Catalog.State.MustRelation(d.Address())
	old := e.Storage.Relations[d.Address()]
	frontier := e.Visibility.CommitSeq()
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := viewcmds.StageMaterializedRows(e, context.Background(), stage, d, old, refreshUnitRows(t, d, 128), true, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	viewcmds.MaterializedStage(stage).Replacement.Concurrent = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counting := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 32}
	// Cancel inside the actual index candidate builder, before any install owner.
	entries, err := index.BuildIndexEntries(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: counting}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, stage.MaterializedOrZero().Replacement.Descriptor, idx.SQLIndex, stage.MaterializedOrZero().Replacement.Storage, nil)
	if entries != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("index preparation ignored cancellation", err)
	}
	if e.Storage.Relations[d.Address()] != old || e.Visibility.CommitSeq() != frontier || old.MaterializedLineage != nil || stage.MaterializedOrZero().Replacement.Storage.MaterializedLineage != nil {
		t.Fatal("canceled index preparation published or stamped")
	}
}

func queryCreateStage(t *testing.T, e *core.Engine, name string, width int) (*core.CatalogStage, *catalog.Relation) {
	t.Helper()
	columns := make([]catalog.Column, width)
	for i := range columns {
		columns[i] = catalog.Column{Name: "i", Usage: enginetest.Usage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}
	}
	e.Mu.Lock()
	d, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), columns, nil)
	stage := e.BeginCatalogStage(e.Catalog.Current())
	if err == nil {
		err = stage.Create(d)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return stage, d
}

// cancelAtErrCheck drives existing cooperative cancellation boundaries without
// sleeps or production hooks. Its embedded real context owns Done and Err state.
type cancelAtErrCheck struct {
	context.Context
	cancel context.CancelFunc
	at     int32
	checks atomic.Int32
}

func (c *cancelAtErrCheck) Err() error {
	if c.checks.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

// Reduced storage fixtures still need real family members and genuine callable
// declarations. Share an immutable ordinary engine bootstrap, never a provider
// fallback or a test callback.
func operatorFamilyTestRead() catalog.Read { return operatorFamilyUnitSeed() }

var operatorFamilyUnitSeed = sync.OnceValue(func() catalog.Read { return engine.New().CatalogRead() })
