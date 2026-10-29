package core_test

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func TestExtensionDependencyDescriptions(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "uuid-ossp")
	view := e.CatalogRead()
	for _, path := range [][]ids.ObjectAddress{
		nil,
		{},
		{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}},
	} {
		if got := stored.DependencyObject(view, ext.Address(), path); got != "extension uuid-ossp" {
			t.Fatalf("extension description on path %v: got %q", path, got)
		}
	}
}
