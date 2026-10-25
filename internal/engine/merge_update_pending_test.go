package engine

import (
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

// These are executor/Finish and manager-state witnesses, not public SQL copies.
// A live request on the original RowID establishes the exact interleaving.
func mergeUpdatePendingRow(t *testing.T, e *core.Engine, ref ids.ObjectRef, row ids.RowID, worker *session.Session, blocker *lock.Ticket, mode lock.Mode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: row}]
		found := false
		if entry != nil && entry.Holders[blocker].Mode != 0 {
			for _, request := range entry.Pending {
				if !request.Terminal && request.Ticket.Ref().Session() == worker.SessionToken && (mode == 0 || request.Mode == mode) {
					found = true
					break
				}
			}
		}
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("missing exact live MERGE row request", row, mode)
		}
		runtime.Gosched()
	}
}
