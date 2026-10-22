package analyze

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestRelationNamespaceShape(t *testing.T) {
	nsAddress := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}
	relAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}
	state := catalog.NewState(1)
	state.SetDescriptor(nsAddress, &catalog.Namespace{ObjectAddress: nsAddress, ObjectName: "s"})
	relation := &catalog.Relation{ObjectAddress: relAddress, ObjectName: "t", ObjectNamespace: nsAddress, Columns: []catalog.Column{{Name: "a"}, {Name: "b"}}}
	state.SetDescriptor(relAddress, relation)
	ref := ids.ObjectRef{Address: relAddress, Epoch: 1}

	for _, leaf := range []int{0, QueryTargetLeaf} {
		ns := RelationNamespace(catalog.NewRead(nil, state), relation, ref, leaf)
		if len(ns.Columns) != 2 || len(ns.Qualifiers) != 1 || len(ns.Frame) != 2 || &ns.Frame[0] != &relation.Columns[0] {
			t.Fatalf("leaf %d: namespace = %+v", leaf, ns)
		}
		for i, column := range ns.Columns {
			if column.Descriptor.Name != relation.Columns[i].Name || column.Slot != i || column.Physical != i || column.Leaf != leaf {
				t.Fatalf("leaf %d: column %d = %+v", leaf, i, column)
			}
		}
		q := ns.Qualifiers[0]
		if q.Name != "t" || q.Original != "t" || q.Schema != "s" || q.Alias != "" || q.Ref != ref || len(q.Columns) != 2 || &q.Columns[0] != &ns.Columns[0] {
			t.Fatalf("leaf %d: qualifier = %+v", leaf, q)
		}
	}
}
