package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

type indexMaintenanceSnapshot struct {
	catalog *catalog.State
	roots   map[ids.ObjectAddress]*storage.IndexRoot
	stores  map[ids.ObjectAddress]*storage.Relation
}

func indexMaintenanceCapture(e *core.Engine) indexMaintenanceSnapshot {
	s := indexMaintenanceSnapshot{catalog: e.Catalog.State, roots: make(map[ids.ObjectAddress]*storage.IndexRoot, len(e.Storage.IndexRoots)), stores: make(map[ids.ObjectAddress]*storage.Relation, len(e.Storage.Relations))}
	for a, r := range e.Storage.IndexRoots {
		s.roots[a] = r
	}
	for a, r := range e.Storage.Relations {
		s.stores[a] = r
	}
	return s
}

func indexMaintenanceUnchanged(t *testing.T, e *core.Engine, before indexMaintenanceSnapshot) {
	t.Helper()
	if e.Catalog.State != before.catalog || len(e.Storage.IndexRoots) != len(before.roots) || len(e.Storage.Relations) != len(before.stores) {
		t.Fatal("failed operation published catalog/storage/root state")
	}
	for a, r := range before.roots {
		if e.Storage.IndexRoots[a] != r {
			t.Fatal("failed operation changed root", a)
		}
	}
	for a, r := range before.stores {
		if e.Storage.Relations[a] != r {
			t.Fatal("failed operation changed table storage", a)
		}
	}
}
