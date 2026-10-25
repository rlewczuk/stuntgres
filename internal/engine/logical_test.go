package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/sqltype"
)

func eType(t *testing.T, e *core.Engine, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	typ, ok := e.CatalogRead().LookupType(id)
	if !ok {
		t.Fatal(id)
	}
	return typ
}
