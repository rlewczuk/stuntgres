package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

func pgAttributeUnitSource(t *testing.T, e *core.Engine, catalog catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1249}
	d, ok := catalog.Relation(address)
	if !ok {
		t.Fatal("missing pg_attribute")
	}
	return d, e.Catalog.Domain.Ref(catalog.Epoch(), address)
}
