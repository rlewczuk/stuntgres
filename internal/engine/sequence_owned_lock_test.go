package engine

import (
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func ownedLockQueued(t *testing.T, e *core.Engine, ref ids.ObjectRef, mode lock.Mode, done <-chan error) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		var request *lock.Request
		if entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == mode {
			request = entry.Pending[0].Request
		}
		if request != nil {
			return request
		}
		select {
		case err := <-done:
			t.Fatalf("OWNED BY operation completed before required %d admission on %v: %v", mode, ref, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("required exact relation admission not observed")
		}
		runtime.Gosched()
	}
}
