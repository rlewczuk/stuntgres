package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDDLHostStageSchemaTableKeepsAuthority(t *testing.T) {
	e := New()
	admin, adminDone := roleOperation(t, e, "postgres")
	defer adminDone()
	roleExecute(t, e, admin, "CREATE ROLE schema_element_owner LOGIN")
	owner, err := e.ResolveLogin("schema_element_owner")
	if err != nil {
		t.Fatal(err)
	}
	op, auth := conversionUnitAuth(t, e)
	table := statement(t, "CREATE TABLE element(id INT)").(*parser.CreateTable)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := DDLHost{E: e, Op: op, CatalogStage: stage}
	oid, err := h.AllocateOID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := h.Revision()
	if err != nil {
		t.Fatal(err)
	}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}, ObjectName: "elements", ObjectRevision: revision, Owner: owner}
	if err := h.Stage().Create(ns); err != nil {
		t.Fatal(err)
	}
	user := auth.State.CurrentUser
	if err := h.StageSchemaTable(auth, ns.Address(), owner, table, sqltype.InputEnvironment{}); err != nil {
		t.Fatal(err)
	}
	if auth.State.CurrentUser != user || auth.pathPrefix != (ids.ObjectAddress{}) {
		t.Fatal("the caller's authority was changed")
	}
	address, ok := stage.NameBinding(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address(), Name: "element"})
	if !ok {
		t.Fatal("no table was staged in the namespace")
	}
	if r, ok := e.StageRead(stage).Relation(address); !ok || r.Owner != owner || r.Namespace() != ns.Address() {
		t.Fatal("the staged table has the wrong owner or namespace", ok)
	}
}
