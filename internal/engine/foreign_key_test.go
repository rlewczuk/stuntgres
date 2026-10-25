package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func foreignKeyLockFixture(t *testing.T) (*Engine, *session.Session, *session.Session, *session.Session, ids.ObjectRef, ids.RowID) {
	t.Helper()
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, c, "CREATE TABLE p(id INT PRIMARY KEY,note INT)")
	sessionSQL(t, c, "CREATE TABLE c(pid INT REFERENCES p)")
	sessionSQL(t, c, "INSERT INTO p VALUES(1,10)")
	ref := catalogRef(t, e, e.CatalogRead(), "p")
	e.Mu.Lock()
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	e.Mu.Unlock()
	return e, a, b, c, ref, row
}

func foreignKeyHeldMode(t *testing.T, e *Engine, ref ids.ObjectRef, row ids.RowID, ticket *lock.Ticket, want lock.Mode) {
	t.Helper()
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: row}]
	var actual lock.Mode
	if entry != nil {
		actual = entry.Holders[ticket].Mode
	}
	if actual != want {
		t.Fatal("exact canonical parent row grant", ref, row, actual, want)
	}
}

func foreignKeyQueuedMode(t *testing.T, e *Engine, ref ids.ObjectRef, row ids.RowID, holder *lock.Ticket, want lock.Mode) {
	t.Helper()
	rowLockQueued(t, e, holder, want)
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: row}]
	found := false
	if entry != nil {
		for _, request := range entry.Pending {
			found = found || !request.Terminal && request.Mode == want && request.Ticket != holder
		}
	}
	if !found {
		t.Fatal("request queued on another/fabricated relation or row", ref, row, want)
	}
}
