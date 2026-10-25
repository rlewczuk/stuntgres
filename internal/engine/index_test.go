package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func btreeRoot(t *testing.T, e *core.Engine, name string) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	ref := roleTableRef(t, e, name)
	idx := e.Catalog.State.MustRelation(ref.Address)
	root := e.Storage.IndexRoots[ref.Address]
	if idx.RelationKind != catalog.IndexRelation || idx.SQLIndex == nil || root == nil || e.Storage.Relations[ref.Address] != nil {
		t.Fatal("not a substantive index", idx, root)
	}
	if err := index.ValidateIndexDefinition(e.CatalogRead(), idx); err != nil {
		t.Fatal(err)
	}
	return idx, root
}

func btreeKeys(t *testing.T, root *storage.IndexRoot) []string {
	t.Helper()
	keys := make([]string, len(root.Entries))
	identities := make(map[ids.VersionID]bool)
	for i, entry := range root.Entries {
		if entry.Row == 0 || entry.Version == 0 || identities[entry.Version] {
			t.Fatal("collapsed/invalid identity", entry)
		}
		identities[entry.Version] = true
		value := entry.Keys[0]
		if value.IsNull() {
			keys[i] = "NULL"
		} else {
			var err error
			keys[i], err = value.Type().Output(context.Background(), value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return keys
}

func btreeWantKeys(t *testing.T, e *core.Engine, name string, want ...string) *storage.IndexRoot {
	t.Helper()
	_, root := btreeRoot(t, e, name)
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, want) {
		t.Fatal(name, got, want)
	}
	return root
}
