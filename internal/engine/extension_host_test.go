package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

// Provider staging reaches the catalog stage only through extensionHost. The
// adapter keeps CatalogStage's lifecycle check: a discarded stage refuses new
// descriptors instead of reaching the unchecked candidate underneath.
func TestExtensionHostRefusesDiscardedStage(t *testing.T) {
	e := New()
	before := e.CatalogRead()
	stage := e.BeginCatalogStage(before)
	host := &extensionHost{engine: e, stage: stage}
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 200000}, ObjectName: "probe", Version: "1.0", TargetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Owner: e.roles.domain.Principal(10)}
	if host.Read().State() != before.State() {
		t.Fatal("host does not read the stage base")
	}
	e.DiscardCatalogStage(stage)
	e.mu.Lock()
	err := host.Create(ext)
	e.mu.Unlock()
	if err == nil {
		t.Fatal("discarded stage accepted a descriptor")
	}
	if host.LookupName(catalog.DescriptorNameKey(ext)) != (ids.ObjectAddress{}) || e.catalog.state != before.State() {
		t.Fatal("refused descriptor reached the stage or the published catalog")
	}
}
