package engine

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func eType(t *testing.T, e *Engine, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	typ, ok := e.CatalogRead().LookupType(id)
	if !ok {
		t.Fatal(id)
	}
	return typ
}
