package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPartitionAttachIndexBuild(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "populated_mapped_backing", true: "duplicate_preserves_originals"}[duplicate], func(t *testing.T) {
			f := partitionConstraintDescriptors(t)
			// Keep an unrelated, independently usable local backing on the same
			// candidate. The new parent-derived unique backing must not replace it.
			local := *f.child
			localDefinition := *local.sqlIndex
			localDefinition.unique = false
			local.sqlIndex = &localDefinition
			current := cloneCatalog(f.e.state)
			current.descriptors[local.address] = &local
			f.e.state = current
			mapped, err := mapPartitionIndex(context.Background(), CatalogRead{owner: f.e, state: current}, f.root, f.leaf, f.parent.sqlIndex)
			if err != nil {
				t.Fatal(err)
			}
			mapped.valid, mapped.ready, mapped.live = true, true, true
			mapped.partitionParent = f.parent.address
			backing := *f.child
			backing.address.ObjectID = 46300
			backing.name = "attach_built_unique"
			backing.sqlIndex = mapped
			backing.columns = slices.Clone(f.child.columns)
			backing.attributes = slices.Clone(f.child.attributes)
			next := cloneCatalog(current)
			next.descriptors[backing.address] = &backing
			next.names[descriptorNameKey(&backing)] = backing.address
			// Completing the only child's backing makes the virtual parent
			// valid in this private candidate, without changing its old head.
			parentIndex := *f.parent
			parentDefinition := *f.parent.sqlIndex
			parentDefinition.valid = true
			parentIndex.sqlIndex = &parentDefinition
			next.descriptors[parentIndex.address] = &parentIndex
			integer := func(slot int, n int64) sqltype.Value {
				value, err := sqltype.Integer(f.leaf.columns[slot].Usage.Type, n)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			second := int64(2)
			if duplicate {
				second = 1
			}
			store := &relationStorage{generation: 4, versions: []rowVersion{
				{row: 1, version: 1, born: 1, values: []sqltype.Value{integer(0, 7), integer(1, 1), sqltype.String(f.leaf.columns[2].Usage.Type, "first")}},
				{row: 2, version: 2, born: 1, values: []sqltype.Value{integer(0, 7), integer(1, second), sqltype.String(f.leaf.columns[2].Usage.Type, "different included payload")}},
			}}
			f.e.storage[f.leaf.address] = store
			oldRoot, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, f.leaf, local.sqlIndex, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.e.indexRoots = map[ids.ObjectAddress]*logicalIndexRoot{local.address: oldRoot}
			beforeStore := uniquePublicationStore(store)
			beforeEntries := slices.Clone(oldRoot.entries)
			beforeCatalog, beforeRoots := f.e.state, maps.Clone(f.e.indexRoots)
			captured := captureIndexStores(f.e.storage, next)
			roots, err := f.e.prepareIndexPublication(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, current, next, captured, f.e.indexRoots, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil)
			if f.e.state != beforeCatalog || !maps.Equal(f.e.indexRoots, beforeRoots) || !reflect.DeepEqual(*store, beforeStore) || !reflect.DeepEqual(oldRoot.entries, beforeEntries) {
				t.Fatal("private index build mutated original catalog/store/backing")
			}
			if duplicate {
				var diagnostic *sqlstate.Error
				if roots != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Message != `could not create unique index "attach_built_unique"` || diagnostic.Constraint != backing.name {
					t.Fatal("duplicate build returned partial roots or wrong failure", roots, err)
				}
				if f.e.state.descriptors[backing.address] != nil {
					t.Fatal("failed backing escaped into live catalog")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			built := roots[backing.address]
			if built == nil || roots[local.address] != oldRoot || roots[f.parent.address] != nil || len(built.entries) != 2 {
				t.Fatal("backing omitted real populated rows, replaced local index, or materialized virtual root", roots)
			}
			for i, entry := range built.entries {
				first, err := entry.keys[0].Integer()
				if err != nil {
					t.Fatal(err)
				}
				second, err := entry.keys[1].Integer()
				if err != nil {
					t.Fatal(err)
				}
				if entry.row != ids.RowID(i+1) || entry.version != ids.VersionID(i+1) || first != int64(i+1) || second != 7 || len(entry.keys) != 2 {
					t.Fatal("physical attribute mapping or INCLUDE uniqueness changed", entry)
				}
			}
			if backing.sqlIndex.partitionParent != f.parent.address || local.sqlIndex.partitionParent != (ids.ObjectAddress{}) {
				t.Fatal("new backing lost ancestry or hijacked unrelated local index")
			}
		})
	}
}
