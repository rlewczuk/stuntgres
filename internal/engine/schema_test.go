package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

func schemaRef(t *testing.T, e *core.Engine, name string) ids.ObjectRef {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
	if a == (ids.ObjectAddress{}) {
		t.Fatal("missing namespace", name)
	}
	return e.Catalog.Domain.Ref(v.Epoch(), a)
}
