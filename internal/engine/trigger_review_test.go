package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This tree owns genuine row descriptors and stores; no SQL/driver scenario or
// fake trigger provider is used to distinguish batching from accepted prefixes.
func triggerPartitionUnitTree(t *testing.T, e *core.Engine, model *catalog.Relation) (*catalog.Relation, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	class, _ := sqltype.DefaultHashIndexClass(integer)
	hash, err := operatorFamilyTestPartitionHash(integer.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	var relations [3]*catalog.Relation
	e.Mu.Lock()
	state := catalog.CloneState(e.Catalog.State)
	for i, name := range []string{"trigger_partition_root", "trigger_partition_low", "trigger_partition_high"} {
		d := *model
		d.ObjectAddress.ObjectID = 9500101 + ids.OID(i)
		d.ObjectName = name
		d.RowType = 9500201 + sqltype.ID(i)
		d.Triggers = nil
		members := []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 2, Name: "v", Usage: integer.Usage()}}
		shape, failure := sqltype.Composite(d.RowType, name, "public."+name, 0, members)
		if failure != nil {
			e.Mu.Unlock()
			t.Fatal(failure)
		}
		rowtype := &catalog.Type{SQLType: shape, ObjectNamespace: d.Namespace(), ObjectRevision: 1, Owner: d.Owner, OwningRelation: d.Address()}
		state.SetDescriptor(rowtype.Address(), rowtype)
		state.BindName(catalog.DescriptorNameKey(rowtype), rowtype.Address())
		relations[i] = &d
	}
	root, low, high := relations[0], relations[1], relations[2]
	root.RelationKind = catalog.PartitionedRelation
	root.History.HasSubclasses = true
	root.Partition = &catalog.PartitionDefinition{Strategy: parser.PartitionHash, Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: integer.Usage(), Class: class, Hash: hash, Label: "id"}}, Children: []ids.ObjectAddress{low.Address(), high.Address()}}
	low.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 4, Remainder: 0}
	high.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 4, Remainder: 2}
	for _, d := range relations {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	e.Catalog.PublishLocked(state)
	e.Storage.Relations[low.Address()] = &storage.Relation{Generation: 1}
	e.Storage.Relations[high.Address()] = &storage.Relation{Generation: 1}
	e.Mu.Unlock()
	return root, low, high
}
