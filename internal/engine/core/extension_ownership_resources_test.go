package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestExtensionOwnershipResources(t *testing.T) {
	e, owner, installer, ext, closeOwner := extensionTemporaryInstalled(t)
	for _, q := range []string{`CREATE FUNCTION public.eo_survivor(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`, `ALTER EXTENSION unaccent ADD FUNCTION public.eo_survivor(integer)`, `ALTER EXTENSION unaccent DROP FUNCTION public.eo_survivor(integer)`} {
		enginetest.RoleExecute(t, e, installer, q)
	}
	base := e.CatalogRead()
	survivorAddress := base.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "eo_survivor", Signature: "23"})
	survivor := base.MustProcedure(survivorAddress)
	if catalog.ExtensionMembership(survivor) != (ids.ObjectAddress{}) || survivor.Implementation != nil {
		t.Fatal("ordinary detached member acquired provider authority")
	}
	peer, closePeer := extensionTemporaryOperation(t, e, "postgres")
	defer closePeer()
	enginetest.RoleExecute(t, e, peer, `CREATE TEMP TABLE eo_unrelated(v integer)`)
	peerNamespace := peer.Session.TemporaryNamespace
	peerTable := e.CatalogRead().MustRelation(e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peerNamespace, Name: "eo_unrelated"}))
	peerStore := e.Storage.Relations[peerTable.Address()]
	// Capture a genuine terminal stage, then change current membership. Its
	// observed ext descriptor/predecessor must fence the stale retirement.
	currentExt := e.CatalogRead().MustExtension(ext.Address())
	stage, indexes := extensionTemporaryStage(t, e, currentExt)
	enginetest.RoleExecute(t, e, installer, `CREATE FUNCTION public.eo_late(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`)
	enginetest.RoleExecute(t, e, installer, `ALTER EXTENSION unaccent ADD FUNCTION public.eo_late(integer)`)
	latest := e.CatalogRead()
	lateAddress := latest.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "eo_late", Signature: "23"})
	e.Mu.Lock()
	installed, err := core.EngineInstallTemporarySessionStageLocked(e, owner.Session, stage, []ids.ObjectAddress{ext.TargetNamespace}, indexes)
	e.Mu.Unlock()
	if err != nil || installed {
		t.Fatal("stale membership retirement published", installed, err)
	}
	if e.CatalogRead().Get(lateAddress) != latest.Get(lateAddress) || e.CatalogRead().Get(currentExt.Address()) != latest.Get(currentExt.Address()) {
		t.Fatal("rejected retirement changed live owners")
	}
	closeOwner()
	view := e.CatalogRead()
	if view.Get(ext.Address()) != nil || view.Get(lateAddress) != nil {
		t.Fatal("physical close failed current membership closure")
	}
	if view.Get(survivor.Address()) != survivor || view.Get(peerNamespace) == nil || view.Get(peerTable.Address()) != peerTable || e.Storage.Relations[peerTable.Address()] != peerStore {
		t.Fatal("drain retired detached survivor or unrelated physical owner")
	}
	for _, d := range view.Descriptors() {
		if d.Namespace() == ext.TargetNamespace {
			t.Fatal("retired physical namespace retained child", d.Address())
		}
	}
	for _, a := range ext.Members {
		if base.Get(a) == nil {
			t.Fatal("drain mutated captured immutable catalog")
		}
	}
}
