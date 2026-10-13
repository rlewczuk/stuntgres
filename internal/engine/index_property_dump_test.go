package engine

import (
	"context"
	"reflect"
	"testing"
)

func TestIndexPropertyDumpPredecessors(t *testing.T) {
	view := New().CatalogRead()
	parentAddress := ObjectAddress{ClassID: RelationClassID, ObjectID: 100010}
	indexAddress := ObjectAddress{ClassID: RelationClassID, ObjectID: 100001}
	consumer := ObjectAddress{ClassID: RelationClassID, ObjectID: 100002}
	namespace := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	parent := &relationDescriptor{address: parentAddress, namespace: namespace, relationKind: tableRelation}
	index := &relationDescriptor{address: indexAddress, namespace: namespace, relationKind: indexRelation, sqlIndex: &indexDefinition{parent: parentAddress, ready: true, valid: true, live: true}}
	view.state.descriptors[parentAddress] = parent
	view.state.descriptors[indexAddress] = index
	got, err := dumpPredecessor(view, indexAddress, consumer)
	if err != nil || got != indexAddress.ObjectID {
		t.Fatal("ordinary index lost exact predecessor", got, err)
	}
	constraint := uniqueConstraint{OID: 100020, Index: indexAddress}
	index.sqlIndex.unique, index.sqlIndex.constraint = true, uniqueBacking(&constraint)
	parent.uniqueConstraints = []uniqueConstraint{constraint}
	got, err = dumpPredecessor(view, indexAddress, consumer)
	if err != nil || got != parentAddress.ObjectID {
		t.Fatal("inline unique predecessor", got, err)
	}
	got, err = dumpPredecessor(view, indexAddress, parentAddress)
	if err != nil || got != 0 {
		t.Fatal("inline self predecessor", got, err)
	}
	parent.uniqueConstraints = nil
	if _, err := dumpPredecessor(view, indexAddress, consumer); err == nil {
		t.Fatal("orphan unique dependency accepted")
	}
}

func TestIndexPropertyDumpRemappedTopology(t *testing.T) {
	// Destination OIDs deliberately put the dependent view and index before
	// their parent. Creation must still follow definition dependencies.
	graph := map[dumpEventKey][]dumpEventKey{{oid: 30}: nil, {oid: 20}: {{oid: 30}}, {oid: 10}: {{oid: 20}}, {oid: 40}: {{oid: 20}}}
	order, err := viewDumpOrder(context.Background(), graph, nil)
	if err != nil || !reflect.DeepEqual(dumpDeclarationIDs(order), []OID{30, 20, 10, 40}) {
		t.Fatal(order, err)
	}
	// A CHECK on the parent requiring its own ordinary index is a real cycle;
	// this feature adds no successful constraint-deferral fallback.
	if _, err := viewDumpOrder(context.Background(), map[dumpEventKey][]dumpEventKey{{oid: 30}: {{oid: 20}}, {oid: 20}: {{oid: 30}}}, nil); err == nil {
		t.Fatal("cyclic index definition dependencies accepted")
	}
}
