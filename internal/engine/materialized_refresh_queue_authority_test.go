package engine

import (
	"context"
	"errors"
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"testing"
	"time"
)

func TestMaterializedRefreshQueuedAuthority(t *testing.T) { materializedQueuedAuthority(t, false) }
func TestMaterializedRefreshConcurrentQueuedAuthority(t *testing.T) {
	materializedQueuedAuthority(t, true)
}

func materializedQueuedAuthority(t *testing.T, concurrent bool) {
	for _, phase := range []string{"owner", "schema", "name", "columns", "attributes", "rowtype", "definition", "ref", "epoch"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			if concurrent {
				materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			}
			oldStorage := e.storage[d.Address()]
			originalEpoch := e.catalog.state.Epoch()
			holder := sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			ref := e.catalog.Domain.Ref(originalEpoch, d.Address())
			mode := lock.AccessShare
			if concurrent {
				mode = lock.Exclusive
			}
			if err := e.locks.Acquire(context.Background(), holder.transactions.ticket, lock.Resource{ObjectRef: ref}, mode); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			scope := refreshUnitScope(t, e, ctx)
			stmt := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}}
			if concurrent {
				stmt.Concurrent = parser.Position{Character: 27}
			}
			done := make(chan error, 1)
			go func() { done <- scope.acquireMaterializedRefresh(stmt) }()
			joined := false
			defer func() {
				cancel()
				e.mu.Lock()
				e.catalog.state.SetEpoch(originalEpoch)
				e.mu.Unlock()
				e.endLockOwner(holder.transactions.ticket)
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("queued authority worker did not join")
					}
				}
			}()
			lockPending(t, e, 1)
			// Mutate authoritative immutable catalog roots while the actual request
			// is pending. These are package fixtures, not added SQL ALTER capabilities.
			e.mu.Lock()
			nextState := *e.catalog.state
			nextState.cloneDescriptors()
			nextState.cloneNames()
			next := *d
			next.ObjectRevision++
			expectedAddress := d.Address()
			expectedStorage := oldStorage
			switch phase {
			case "owner":
				owner, _ := refreshUnitPrincipals(e)
				next.Owner = owner
			case "schema":
				next.ObjectNamespace.ObjectID++
			case "name":
				next.ObjectName = "changed_name"
			case "columns":
				next.Columns = append([]ColumnDescriptor(nil), d.Columns...)
				next.Columns[0].Name = "changed_shape"
			case "attributes":
				next.Attributes = append([]int32(nil), d.Attributes...)
				next.Attributes[0]++
			case "rowtype":
				next.RowType++
			case "definition":
				definition := *viewOf(d)
				definition.invoker = !definition.invoker
				next.View = &definition
			case "ref":
				next.ObjectAddress.ObjectID += 100000
				expectedAddress = next.Address()
				nextState.removeDescriptor(d.Address())
				nextState.bindName(ids.NameKey{ClassID: d.Address().ClassID, Namespace: d.Namespace(), Name: d.Name()}, next.Address())
				storage := *oldStorage
				expectedStorage = &storage
				e.storage = maps.Clone(e.storage)
				delete(e.storage, d.Address())
				e.storage[next.Address()] = expectedStorage
			case "epoch":
				nextState.SetEpoch(nextState.Epoch() + 1)
			}
			nextState.setDescriptor(expectedAddress, &next)
			e.catalog.state = &nextState
			before, rowCounter, versionCounter := e.commitSeq, e.nextRow, e.nextVersion
			e.mu.Unlock()
			e.endLockOwner(holder.transactions.ticket)
			var err error
			select {
			case err = <-done:
				joined = true
			case <-ctx.Done():
				t.Fatal("queued authority join", ctx.Err())
			}
			if !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("changed frozen authority was admitted", phase, err)
			}
			if scope.lockedDescriptor != nil || scope.lockedStorage != nil || scope.lockedRef != (ids.ObjectRef{}) || len(scope.materializedSources) != 0 {
				t.Fatal("rejected queue captured or retargeted a producer")
			}
			if e.catalog.state.Get(expectedAddress) != &next || e.storage[expectedAddress] != expectedStorage || len(expectedStorage.versions) != 3 || e.commitSeq != before || e.nextRow != rowCounter || e.nextVersion != versionCounter || !next.Populated {
				t.Fatal("rejected queue changed current metadata/store/frontier/counters")
			}
			// Restore only the fixture epoch before normal session/transaction cleanup.
			e.mu.Lock()
			e.catalog.state.SetEpoch(originalEpoch)
			e.mu.Unlock()
			scope.Finish(err)
			transactionEnd(t, holder, tx, false)
			lockEmpty(t, e)
		})
	}
}
