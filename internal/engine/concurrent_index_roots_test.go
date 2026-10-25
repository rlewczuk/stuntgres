package engine

import (
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// The phase witness is an actual completion request owned by the builder, not
// worker start, a timing delay, or descriptor visibility standing in for a queue.
func concurrentUnitCompletion(t *testing.T, e *Engine, owner *lock.Ticket, builder *session.Session) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		var found *lock.Request
		if entry := lockState.Resources[lock.CompletionOf(owner)]; entry != nil {
			for _, request := range entry.Pending {
				if !request.Terminal && request.Ticket.Ref().Session() == builder.SessionToken && request.Mode == lock.TransactionCompletion {
					found = request.Request
					break
				}
			}
		}
		if found != nil {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatal("builder did not journal exact transaction completion wait")
		}
		runtime.Gosched()
	}
}

func concurrentUnitIndex(t *testing.T, e *Engine, name string, ready, valid bool) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	index, ok := e.Catalog.State.Relation(address)
	if !ok || index.SQLIndex == nil || index.SQLIndex.Ready != ready || index.SQLIndex.Valid != valid || !index.SQLIndex.Live {
		t.Fatalf("index %s has unexpected phase: %#v", name, index)
	}
	return index, e.Storage.IndexRoots[address]
}
