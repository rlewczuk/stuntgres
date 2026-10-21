package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTableHostBindColumnDefaultsMatchesCreateTable(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	s := statement(t, "CREATE TABLE host_defaults(a int DEFAULT 1 + 2, b text DEFAULT 'x' || 'y', c int)").(*parser.CreateTable)
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	e.mu.Lock()
	defer e.mu.Unlock()

	created := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(created)
	if err := e.schemaTableLocked(op, auth, created, s, public, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil); err != nil {
		t.Fatal(err)
	}
	address, ok := created.NameBinding(ids.NameKey{ClassID: ids.RelationClassID, Namespace: public, Name: "host_defaults"})
	if !ok {
		t.Fatal("CREATE TABLE staged no relation")
	}
	want := e.stageRead(created).MustRelation(address).ColumnMeta.Defaults

	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	var h table.Host = ddlHost{e: e, op: op, stage: stage}
	b, err := h.DescribeCreateTable(auth, s, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.BindColumnDefaults(auth, b.Create.Columns, b.Create.Declarations, sqltype.InputEnvironment{}, ids.SessionToken{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(want) != 2 {
		t.Fatalf("host bound %d defaults, CREATE TABLE %d; want 2 each", len(got), len(want))
	}
	for attribute, definition := range want {
		definition.OID = 0 // CREATE TABLE assigns OIDs after binding.
		if !reflect.DeepEqual(got[attribute], definition) {
			t.Fatalf("attribute %d: host default %#v, CREATE TABLE default %#v", attribute, got[attribute], definition)
		}
	}
}

func TestTableHostStageChangedReportsStagedDescriptor(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	e.mu.Lock()
	defer e.mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	var h table.Host = ddlHost{e: e, op: op, stage: stage}
	if h.StageChanged() {
		t.Fatal("a new stage reports a change")
	}
	oid, err := h.AllocateOID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := h.Revision()
	if err != nil {
		t.Fatal(err)
	}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}, ObjectName: "host_changed", ObjectRevision: revision, Owner: auth.State.CurrentUser}
	if err := h.Stage().Create(ns); err != nil {
		t.Fatal(err)
	}
	if !h.StageChanged() {
		t.Fatal("a stage holding a descriptor reports no change")
	}
	if address, ok := h.NameBinding(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "host_changed"}); !ok || address != ns.Address() {
		t.Fatalf("NameBinding = %v, %v; want the staged namespace", address, ok)
	}
}
