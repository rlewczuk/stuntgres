package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func vacuumStore(t *testing.T, e *core.Engine, name string) (ids.ObjectRef, *storage.Relation) {
	t.Helper()
	ref := catalogRef(t, e, e.CatalogRead(), name)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	store := e.Storage.Relations[ref.Address]
	if store == nil {
		t.Fatal("missing store", name)
	}
	return ref, store
}
