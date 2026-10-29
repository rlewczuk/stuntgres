package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
)

// Provider staging reaches the catalog stage only through extensionHost. The
// adapter keeps CatalogStage's lifecycle check: a discarded stage refuses new
// descriptors instead of reaching the unchecked candidate underneath.
func TestExtensionHostRefusesDiscardedStage(t *testing.T) {
	e := engine.New()
	before := e.CatalogRead()
	stage := e.BeginCatalogStage(before)
	host := &schemacmds.ExtensionHost{Engine: e, Stage: stage}
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 200000}, ObjectName: "probe", Version: "1.0", TargetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Owner: e.RoleStore.Domain.Principal(10)}
	if host.Read().State() != before.State() {
		t.Fatal("host does not read the stage base")
	}
	e.DiscardCatalogStage(stage)
	e.Mu.Lock()
	err := host.Create(ext)
	e.Mu.Unlock()
	if err == nil {
		t.Fatal("discarded stage accepted a descriptor")
	}
	if host.LookupName(catalog.DescriptorNameKey(ext)) != (ids.ObjectAddress{}) || e.Catalog.State != before.State() {
		t.Fatal("refused descriptor reached the stage or the published catalog")
	}
}

// The handlers in extension stage only through Host.BeginStage. The stage it
// begins reads the captured catalog, carries the statement authority and the
// extension namespace the publication fence observes, and stageOf hands the
// root that same stage back. stageOf refuses a host whose stage was never
// begun and any Stage the root did not make.
func TestExtensionHostBeginStageHandsBackRootStage(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	host := &schemacmds.ExtensionHost{Engine: e, Op: op, Auth: auth}
	if _, err := schemacmds.StageOf(host); err == nil {
		t.Fatal("stageOf accepted a host without a begun stage")
	}
	if _, err := schemacmds.StageOf(foreignExtensionStage{host}); err == nil {
		t.Fatal("stageOf accepted a foreign stage")
	}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	stage, err := schemacmds.StageOf(host.BeginStage(namespace))
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	if stage.Base.State() != auth.Catalog.State() || schemacmds.ExtensionNamespaceOf(stage) != namespace || stage.AuthSession != op.Session || stage.RoleRevision != auth.RoleRecord.Revision {
		t.Fatal("begun stage does not carry the statement's catalog, authority and namespace")
	}
}

type foreignExtensionStage struct{ *schemacmds.ExtensionHost }

func TestExtensionHostRevisesOnlyOpenStagedIdentity(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	host := &schemacmds.ExtensionHost{Engine: e, Stage: stage}
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 200000}, ObjectRevision: 1, ObjectName: "probe", Version: "1.0", TargetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Owner: e.RoleStore.Domain.Principal(10)}
	if err := host.Create(ext); err != nil {
		t.Fatal(err)
	}
	next := *ext
	next.ObjectRevision++
	if err := host.Replace(&next); err != nil {
		t.Fatal("created identity could not acquire staged metadata", err)
	}
	if stage.Get(ext.Address()) != &next || base.Get(ext.Address()) != nil || ext.Revision() != 1 {
		t.Fatal("staged revision mutated the predecessor or published catalog")
	}
	key := catalog.DescriptorNameKey(ext)
	if observed, ok := stage.NameObservation(key); !ok || observed != (ids.ObjectAddress{}) {
		t.Fatal("metadata revision lost CREATE's absence fence")
	}
	renamed := next
	renamed.ObjectName = "other_probe"
	if err := host.Replace(&renamed); err == nil {
		t.Fatal("metadata revision silently rebound a created name")
	}
	missing := next
	missing.ObjectAddress.ObjectID++
	if err := host.Replace(&missing); err == nil {
		t.Fatal("metadata revision created an absent identity")
	}
	e.DiscardCatalogStage(stage)
	if err := host.Replace(ext); err == nil || stage.Get(ext.Address()) != &next {
		t.Fatal("discarded stage accepted a metadata revision", err)
	}
}
