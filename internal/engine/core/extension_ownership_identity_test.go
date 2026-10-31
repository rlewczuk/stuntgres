package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func ownershipUnitInstall(t *testing.T, name string) (*core.Engine, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if name == "earthdistance" {
		enginetest.RoleExecute(t, e, op, "CREATE EXTENSION cube")
	}
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION "+`"`+name+`"`)
	v := e.CatalogRead()
	return e, v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}))
}
