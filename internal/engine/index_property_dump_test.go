package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestIndexPropertyDumpPredecessors(t *testing.T) {
	view := New().CatalogRead()
	parentAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100010}
	indexAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100001}
	consumer := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100002}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	parent := &relationDescriptor{ObjectAddress: parentAddress, ObjectNamespace: namespace, RelationKind: tableRelation}
	index := &relationDescriptor{ObjectAddress: indexAddress, ObjectNamespace: namespace, RelationKind: indexRelation, SQLIndex: &indexDefinition{Parent: parentAddress, Ready: true, Valid: true, Live: true}}
	view.State().SetDescriptor(parentAddress, parent)
	view.State().SetDescriptor(indexAddress, index)
	got, err := dumpPredecessor(view, indexAddress, consumer)
	if err != nil || got != indexAddress.ObjectID {
		t.Fatal("ordinary index lost exact predecessor", got, err)
	}
	constraint := uniqueConstraint{OID: 100020, Index: indexAddress}
	index.SQLIndex.Unique, index.SQLIndex.Constraint = true, uniqueBacking(&constraint)
	parent.UniqueConstraints = []uniqueConstraint{constraint}
	got, err = dumpPredecessor(view, indexAddress, consumer)
	if err != nil || got != parentAddress.ObjectID {
		t.Fatal("inline unique predecessor", got, err)
	}
	got, err = dumpPredecessor(view, indexAddress, parentAddress)
	if err != nil || got != 0 {
		t.Fatal("inline self predecessor", got, err)
	}
	parent.UniqueConstraints = nil
	if _, err := dumpPredecessor(view, indexAddress, consumer); err == nil {
		t.Fatal("orphan unique dependency accepted")
	}
}

func TestIndexPropertyDumpRemappedTopology(t *testing.T) {
	// Destination OIDs deliberately put the dependent view and index before
	// their parent. Creation must still follow definition dependencies.
	graph := map[dumpEventKey][]dumpEventKey{{oid: 30}: nil, {oid: 20}: {{oid: 30}}, {oid: 10}: {{oid: 20}}, {oid: 40}: {{oid: 20}}}
	order, err := viewDumpOrder(context.Background(), graph, nil)
	if err != nil || !reflect.DeepEqual(dumpDeclarationIDs(order), []ids.OID{30, 20, 10, 40}) {
		t.Fatal(order, err)
	}
	// A CHECK on the parent requiring its own ordinary index is a real cycle;
	// this feature adds no successful constraint-deferral fallback.
	if _, err := viewDumpOrder(context.Background(), map[dumpEventKey][]dumpEventKey{{oid: 30}: {{oid: 20}}, {oid: 20}: {{oid: 30}}}, nil); err == nil {
		t.Fatal("cyclic index definition dependencies accepted")
	}
}
