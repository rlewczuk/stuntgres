package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

func TestIndexPropertyDumpPredecessors(t *testing.T) {
	view := New().CatalogRead()
	parentAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100010}
	indexAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100001}
	consumer := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100002}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	parent := &catalog.Relation{ObjectAddress: parentAddress, ObjectNamespace: namespace, RelationKind: catalog.TableRelation}
	idx := &catalog.Relation{ObjectAddress: indexAddress, ObjectNamespace: namespace, RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: parentAddress, Ready: true, Valid: true, Live: true}}
	view.State().SetDescriptor(parentAddress, parent)
	view.State().SetDescriptor(indexAddress, idx)
	got, err := control.Resolver{}.Predecessor(view, indexAddress, consumer)
	if err != nil || got != indexAddress.ObjectID {
		t.Fatal("ordinary index lost exact predecessor", got, err)
	}
	constraint := catalog.UniqueConstraint{OID: 100020, Index: indexAddress}
	idx.SQLIndex.Unique, idx.SQLIndex.Constraint = true, index.UniqueBacking(&constraint)
	parent.Constraints.Unique = []catalog.UniqueConstraint{constraint}
	got, err = control.Resolver{}.Predecessor(view, indexAddress, consumer)
	if err != nil || got != parentAddress.ObjectID {
		t.Fatal("inline unique predecessor", got, err)
	}
	got, err = control.Resolver{}.Predecessor(view, indexAddress, parentAddress)
	if err != nil || got != 0 {
		t.Fatal("inline self predecessor", got, err)
	}
	parent.Constraints.Unique = nil
	if _, err := (control.Resolver{}).Predecessor(view, indexAddress, consumer); err == nil {
		t.Fatal("orphan unique dependency accepted")
	}
}

func TestIndexPropertyDumpRemappedTopology(t *testing.T) {
	// Destination OIDs deliberately put the dependent view and index before
	// their parent. Creation must still follow definition dependencies.
	graph := dump.Graph{{OID: 30}: nil, {OID: 20}: {{OID: 30}}, {OID: 10}: {{OID: 20}}, {OID: 40}: {{OID: 20}}}
	order, err := dump.Order(context.Background(), graph, nil)
	if err != nil || !reflect.DeepEqual(dump.DeclarationIDs(order), []ids.OID{30, 20, 10, 40}) {
		t.Fatal(order, err)
	}
	// A CHECK on the parent requiring its own ordinary index is a real cycle;
	// this feature adds no successful constraint-deferral fallback.
	if _, err := dump.Order(context.Background(), dump.Graph{{OID: 30}: {{OID: 20}}, {OID: 20}: {{OID: 30}}}, nil); err == nil {
		t.Fatal("cyclic index definition dependencies accepted")
	}
}
