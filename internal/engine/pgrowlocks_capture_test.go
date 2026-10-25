package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/lock"
)

func pgrowlocksUnitCapture(t *testing.T, e *core.Engine, caller *lock.Ticket, tuple lock.MultiTuple, private bool) []lock.PgrowlocksRecord {
	t.Helper()
	rows, err := e.Locks.Pgrowlocks(context.Background(), caller, []lock.PgrowlocksTuple{{Tuple: tuple, PrivateInsertion: private}})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
