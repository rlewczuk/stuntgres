package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

func schemaRef(t *testing.T, e *Engine, name string) ids.ObjectRef {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
	if a == (ids.ObjectAddress{}) {
		t.Fatal("missing namespace", name)
	}
	return e.Catalog.Domain.Ref(v.Epoch(), a)
}

type schemaDefaults struct{ path string }

func (d *schemaDefaults) SessionDefault(_ context.Context, definition settings.Definition) (string, bool, error) {
	if definition.Name == "search_path" {
		return d.path, true, nil
	}
	return "", false, nil
}
