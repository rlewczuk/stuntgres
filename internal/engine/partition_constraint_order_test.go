package engine

import (
	"fmt"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
)

// The third pair makes the schema-before-object requirement deterministic even
// when map iteration happens to put the two flattened-name collisions in order.
func TestPartitionDetachConstraintReplayComponentOrder(t *testing.T) {
	permutations := [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}, {1, 2, 0}, {0, 2, 1}, {2, 0, 1}}
	for _, insertion := range permutations {
		t.Run(fmt.Sprint(insertion), func(t *testing.T) {
			e := New()
			state := cloneCatalog(e.catalog.state)
			one := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 49000}, name: "a"}
			two := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 49001}, name: "a.b"}
			state.setDescriptor(one.address, one)
			state.setDescriptor(two.address, two)
			names := []string{"b.c", "z", "c"}
			namespaces := []ids.ObjectAddress{one.address, one.address, two.address}
			var tables, indexes [3]*relationDescriptor
			for _, i := range insertion {
				tableAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(49010 + i)}
				indexAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(49020 + i)}
				tables[i] = &relationDescriptor{address: tableAddress, namespace: namespaces[i], name: names[i] + "_table", partition: &partitionDefinition{}, uniqueConstraints: []uniqueConstraint{{Name: names[i], Index: indexAddress}}}
				indexes[i] = &relationDescriptor{address: indexAddress, namespace: namespaces[i], name: names[i], sqlIndex: &indexDefinition{parent: tableAddress}}
				for _, d := range []*relationDescriptor{tables[i], indexes[i]} {
					state.setDescriptor(d.address, d)
					state.bindName(descriptorNameKey(d), d.address)
				}
			}
			view := e.catalog.read(state)
			_, replay := partitionConstraintDumpCatalog(view)
			// Exact ordinals, not just uniqueness, define canonical table allocation.
			for i, index := range indexes {
				want := fmt.Sprintf("__stuntgres_partition_key_%d", i+1)
				if got := replay.temporary[index.address]; got != want {
					t.Errorf("allocation %s/%s = %q, want %q", view.Get(index.namespace).Name(), tables[i].name, got, want)
				}
			}
			got := replay.finalRenameOrder()
			want := []*relationDescriptor{indexes[0], indexes[1], indexes[2]}
			if !slices.Equal(got, want) {
				var actual []string
				for _, index := range got {
					actual = append(actual, view.Get(index.namespace).Name()+"/"+index.name)
				}
				t.Fatalf("terminal rename order = %v, want [a/b.c a/z a.b/c]", actual)
			}
		})
	}
}
