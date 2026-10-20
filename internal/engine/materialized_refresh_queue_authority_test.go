package engine

import (
	"context"
	"errors"
	"maps"
	"stuntgres/internal/engine/catalog"
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
			oldStorage := e.storage.Relations[d.Address()]
			originalEpoch := e.catalog.state.Epoch()
			holder := sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			ref := e.catalog.Domain.Ref(originalEpoch, d.Address())
			mode := lock.AccessShare
			if concurrent {
				mode = lock.Exclusive
			}
			if err := e.locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, mode); err != nil {
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
				e.endLockOwner(holder.Transactions.Ticket)
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
			nextState.CloneDescriptors()
			nextState.CloneNames()
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
				next.Columns = append([]catalog.Column(nil), d.Columns...)
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
				nextState.RemoveDescriptor(d.Address())
				nextState.BindName(ids.NameKey{ClassID: d.Address().ClassID, Namespace: d.Namespace(), Name: d.Name()}, next.Address())
				storage := *oldStorage
				expectedStorage = &storage
				e.storage.ReplaceRelationsLocked(maps.Clone(e.storage.Relations))
				e.storage.RemoveRelationLocked(d.Address())
				e.storage.InstallRelationLocked(next.Address(), expectedStorage)
			case "epoch":
				nextState.SetEpoch(nextState.Epoch() + 1)
			}
			nextState.SetDescriptor(expectedAddress, &next)
			e.catalog.state = &nextState
			before, rowCounter, versionCounter := e.visibility.CommitSeq(), e.storage.RowFrontier(), e.storage.VersionFrontier()
			e.mu.Unlock()
			e.endLockOwner(holder.Transactions.Ticket)
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
			if scope.lockedDescriptor != nil || scope.lockedStorage != nil || scope.lockedRef != (ids.ObjectRef{}) || len(scope.materializedOrZero().sources) != 0 {
				t.Fatal("rejected queue captured or retargeted a producer")
			}
			if e.catalog.state.Get(expectedAddress) != &next || e.storage.Relations[expectedAddress] != expectedStorage || len(expectedStorage.Versions) != 3 || e.visibility.CommitSeq() != before || e.storage.RowFrontier() != rowCounter || e.storage.VersionFrontier() != versionCounter || !next.Populated {
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
