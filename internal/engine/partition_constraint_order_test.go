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
			one := &namespaceDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 49000}, ObjectName: "a"}
			two := &namespaceDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 49001}, ObjectName: "a.b"}
			state.SetDescriptor(one.Address(), one)
			state.SetDescriptor(two.Address(), two)
			names := []string{"b.c", "z", "c"}
			namespaces := []ids.ObjectAddress{one.Address(), one.Address(), two.Address()}
			var tables, indexes [3]*relationDescriptor
			for _, i := range insertion {
				tableAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(49010 + i)}
				indexAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(49020 + i)}
				tables[i] = &relationDescriptor{ObjectAddress: tableAddress, ObjectNamespace: namespaces[i], ObjectName: names[i] + "_table", Partition: &partitionDefinition{}, UniqueConstraints: []uniqueConstraint{{Name: names[i], Index: indexAddress}}}
				indexes[i] = &relationDescriptor{ObjectAddress: indexAddress, ObjectNamespace: namespaces[i], ObjectName: names[i], SQLIndex: &indexDefinition{Parent: tableAddress}}
				for _, d := range []*relationDescriptor{tables[i], indexes[i]} {
					state.SetDescriptor(d.Address(), d)
					state.BindName(descriptorNameKey(d), d.Address())
				}
			}
			view := e.catalog.read(state)
			_, replay := partitionConstraintDumpCatalog(view)
			// Exact ordinals, not just uniqueness, define canonical table allocation.
			for i, index := range indexes {
				want := fmt.Sprintf("__stuntgres_partition_key_%d", i+1)
				if got := replay.temporary[index.Address()]; got != want {
					t.Errorf("allocation %s/%s = %q, want %q", view.Get(index.Namespace()).Name(), tables[i].Name(), got, want)
				}
			}
			got := replay.finalRenameOrder()
			want := []*relationDescriptor{indexes[0], indexes[1], indexes[2]}
			if !slices.Equal(got, want) {
				var actual []string
				for _, index := range got {
					actual = append(actual, view.Get(index.Namespace()).Name()+"/"+index.Name())
				}
				t.Fatalf("terminal rename order = %v, want [a/b.c a/z a.b/c]", actual)
			}
		})
	}
}
