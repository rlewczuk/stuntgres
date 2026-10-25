package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

// This fixture runs each definition through Engine.Execute in the owner's
// transaction, not through the public SQL adapter.
func definitionUnitStage(t *testing.T, e *core.Engine, owner *session.Session, q string) {
	t.Helper()
	op := transactionOperation(t, owner)
	defer op.Done()
	if _, err := e.Execute(op, statement(t, q), 0, nil); err != nil {
		t.Fatal(err)
	}
}

func definitionUnitRead(e *core.Engine, owner *session.Session) catalog.Read {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.PrivateCatalogLocked(owner.Transactions.Catalog)
}
