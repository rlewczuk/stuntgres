package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

func heapUnitInstalled(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE EXTENSION amcheck")
	return e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "amcheck"}))
}
