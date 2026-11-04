package index

import (
	"iter"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestPreparePartitionConstraintDropDepthFirst(t *testing.T) {
	address := func(oid ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	}
	child := &catalog.Relation{ObjectAddress: address(101)}
	child.Constraints.Unique = []catalog.UniqueConstraint{{OID: 11, Parent: 10, Index: address(201)}}
	grandchild := &catalog.Relation{ObjectAddress: address(102)}
	grandchild.Constraints.Exclusion = []catalog.ExclusionConstraint{{OID: 12, Parent: 11, Index: address(202)}}
	unrelated := &catalog.Relation{ObjectAddress: address(103)}
	unrelated.Constraints.Unique = []catalog.UniqueConstraint{{OID: 13, Parent: 99, Index: address(203)}}
	descriptors := []catalog.Descriptor{child, grandchild, unrelated}
	indexes := map[ids.ObjectAddress]catalog.Descriptor{}
	for _, d := range descriptors {
		indexes[d.Address()] = d
	}
	for _, oid := range []ids.OID{201, 202, 203} {
		indexes[address(oid)] = &catalog.Relation{ObjectAddress: address(oid), SQLIndex: &catalog.IndexDefinition{}}
	}
	input := iter.Seq2[ids.ObjectAddress, catalog.Descriptor](func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for _, d := range descriptors {
			if !yield(d.Address(), d) {
				return
			}
		}
	})
	plan, err := PreparePartitionConstraintDrop(catalog.Read{}, input, func(a ids.ObjectAddress) catalog.Descriptor { return indexes[a] }, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []ids.ObjectAddress
	for _, idx := range plan.Indexes {
		got = append(got, idx.Address())
	}
	if !slices.Equal(got, []ids.ObjectAddress{address(202), address(201)}) {
		t.Fatalf("depth-first unique/exclusion cohort = %v", got)
	}
	delete(indexes, address(202))
	if _, err := PreparePartitionConstraintDrop(catalog.Read{}, input, func(a ids.ObjectAddress) catalog.Descriptor { return indexes[a] }, 10); err != ids.ErrCatalogReplaced {
		t.Fatalf("missing backing identity: %v", err)
	}
}
