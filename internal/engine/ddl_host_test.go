package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestDDLHostRecaptureLeavesArgument(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	e.mu.Lock()
	defer e.mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := ddlHost{e: e, op: op, stage: stage}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}, ObjectName: "recaptured", ObjectRevision: 1, Owner: auth.State.CurrentUser}
	if err := h.Stage().Create(ns); err != nil {
		t.Fatal(err)
	}
	base := auth.Catalog
	next := ddlAuthorityOf(h.Recapture(auth, h.StageRead()))
	if _, ok := next.Catalog.Namespace(ns.Address()); !ok || next.PairedCatalog != next.Catalog {
		t.Fatal("the returned authority does not read the stage")
	}
	if _, ok := auth.Catalog.Namespace(ns.Address()); ok || auth.Catalog != base || auth.PairedCatalog != base {
		t.Fatal("the argument authority was recaptured")
	}
}
