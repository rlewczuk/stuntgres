package engine

import (
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func routinePendingUniqueIndex(t *testing.T, e *Engine, index ids.ObjectRef, worker *session.Session) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		found := false
		for resource, entry := range lockState.Resources {
			if resource.ObjectRef != index {
				continue
			}
			for _, request := range entry.Pending {
				if !request.Terminal && request.Ticket.Ref().Session() == worker.SessionToken && request.Mode == lock.UniqueClaim {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("missing exact actual UNIQUE index waiter", index.Address)
		}
		runtime.Gosched()
	}
}
