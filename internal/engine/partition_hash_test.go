package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// hashPartitionUnitEngine is hashPartitionUnitDescriptor with the engine that
// owns the returned view.
func hashPartitionUnitEngine(t *testing.T) (*Engine, catalog.Read, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	snapshot := catalog.CloneState(e.Catalog.State)
	var typ *sqltype.Descriptor
	for _, candidate := range sqltype.Builtins() {
		if candidate.ID() == sqltype.Int4 {
			typ = candidate
			break
		}
	}
	class, _ := sqltype.DefaultHashIndexClass(typ)
	provider, err := operatorFamilyTestPartitionHash(typ.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	root := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 25000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "hash_root", RelationKind: catalog.PartitionedRelation, Columns: []catalog.Column{{Name: "k", Usage: typ.Usage(), Nullable: true}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"k": 0}}
	leaf := *root
	leaf.ObjectAddress.ObjectID++
	leaf.ObjectName = "hash_leaf"
	leaf.RelationKind = catalog.TableRelation
	leaf.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 1}
	root.Partition = &catalog.PartitionDefinition{Strategy: parser.PartitionHash, Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: typ.Usage(), Class: class, Hash: provider, Label: "k"}}, Children: []ids.ObjectAddress{leaf.Address()}}
	snapshot.SetDescriptor(root.Address(), root)
	snapshot.SetDescriptor(leaf.Address(), &leaf)
	return e, e.Catalog.Read(snapshot), root, &leaf
}
