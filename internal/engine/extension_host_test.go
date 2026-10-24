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
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 200000}, ObjectName: "probe", Version: "1.0", TargetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Owner: e.roles.Domain.Principal(10)}
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
	if host.LookupName(catalog.DescriptorNameKey(ext)) != (ids.ObjectAddress{}) || e.catalog.State != before.State() {
		t.Fatal("refused descriptor reached the stage or the published catalog")
	}
}

// The handlers in extension stage only through Host.BeginStage. The stage it
// begins reads the captured catalog, carries the statement authority and the
// extension namespace the publication fence observes, and stageOf hands the
// root that same stage back. stageOf refuses a host whose stage was never
// begun and any Stage the root did not make.
func TestExtensionHostBeginStageHandsBackRootStage(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	host := &extensionHost{engine: e, op: op, auth: auth}
	if _, err := stageOf(host); err == nil {
		t.Fatal("stageOf accepted a host without a begun stage")
	}
	if _, err := stageOf(foreignExtensionStage{host}); err == nil {
		t.Fatal("stageOf accepted a foreign stage")
	}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	stage, err := stageOf(host.BeginStage(namespace))
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	if stage.Base.State() != auth.Catalog.State() || stage.extensionNamespace != namespace || stage.authSession != op.Session || stage.roleRevision != auth.RoleRecord.Revision {
		t.Fatal("begun stage does not carry the statement's catalog, authority and namespace")
	}
}

type foreignExtensionStage struct{ *extensionHost }
