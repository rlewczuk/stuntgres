package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
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
			localDefinition := *local.SQLIndex
			localDefinition.Unique = false
			local.SQLIndex = &localDefinition
			current := catalog.CloneState(f.e.catalog.state)
			current.SetDescriptor(local.Address(), &local)
			f.e.catalog.state = current
			mapped, err := mapPartitionIndex(context.Background(), f.e.catalog.read(current), f.root, f.leaf, f.parent.SQLIndex)
			if err != nil {
				t.Fatal(err)
			}
			mapped.Valid, mapped.Ready, mapped.Live = true, true, true
			mapped.PartitionParent = f.parent.Address()
			backing := *f.child
			backing.ObjectAddress.ObjectID = 46300
			backing.ObjectName = "attach_built_unique"
			backing.SQLIndex = mapped
			backing.Columns = slices.Clone(f.child.Columns)
			backing.Attributes = slices.Clone(f.child.Attributes)
			next := catalog.CloneState(current)
			next.SetDescriptor(backing.Address(), &backing)
			next.BindName(catalog.DescriptorNameKey(&backing), backing.Address())
			// Completing the only child's backing makes the virtual parent
			// valid in this private candidate, without changing its old head.
			parentIndex := *f.parent
			parentDefinition := *f.parent.SQLIndex
			parentDefinition.Valid = true
			parentIndex.SQLIndex = &parentDefinition
			next.SetDescriptor(parentIndex.Address(), &parentIndex)
			integer := func(slot int, n int64) sqltype.Value {
				value, err := sqltype.Integer(f.leaf.Columns[slot].Usage.Type, n)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			second := int64(2)
			if duplicate {
				second = 1
			}
			store := &storage.Relation{Generation: 4, Versions: []storage.RowVersion{
				{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{integer(0, 7), integer(1, 1), sqltype.String(f.leaf.Columns[2].Usage.Type, "first")}},
				{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{integer(0, 7), integer(1, second), sqltype.String(f.leaf.Columns[2].Usage.Type, "different included payload")}},
			}}
			f.e.mu.Lock()
			f.e.storage.InstallRelationLocked(f.leaf.Address(), store)
			f.e.mu.Unlock()
			oldRoot, err := buildIndexRoot(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Scope: 1, Frame: 1}}, f.leaf, local.SQLIndex, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.e.mu.Lock()
			f.e.storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{local.Address(): oldRoot})
			f.e.mu.Unlock()
			beforeStore := uniquePublicationStore(store)
			beforeEntries := slices.Clone(oldRoot.Entries)
			beforeCatalog, beforeRoots := f.e.catalog.state, maps.Clone(f.e.storage.IndexRoots)
			captured := captureIndexStores(f.e.storage.Relations, next)
			roots, err := f.e.prepareIndexPublication(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Scope: 1, Frame: 1}}, current, next, captured, f.e.storage.IndexRoots, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil)
			if f.e.catalog.state != beforeCatalog || !maps.Equal(f.e.storage.IndexRoots, beforeRoots) || !reflect.DeepEqual(*store, beforeStore) || !reflect.DeepEqual(oldRoot.Entries, beforeEntries) {
				t.Fatal("private index build mutated original catalog/store/backing")
			}
			if duplicate {
				var diagnostic *sqlstate.Error
				if roots != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Message != `could not create unique index "attach_built_unique"` || diagnostic.Constraint != backing.Name() {
					t.Fatal("duplicate build returned partial roots or wrong failure", roots, err)
				}
				if f.e.catalog.state.Get(backing.Address()) != nil {
					t.Fatal("failed backing escaped into live catalog")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			built := roots[backing.Address()]
			if built == nil || roots[local.Address()] != oldRoot || roots[f.parent.Address()] != nil || len(built.Entries) != 2 {
				t.Fatal("backing omitted real populated rows, replaced local index, or materialized virtual root", roots)
			}
			for i, entry := range built.Entries {
				first, err := entry.Keys[0].Integer()
				if err != nil {
					t.Fatal(err)
				}
				second, err := entry.Keys[1].Integer()
				if err != nil {
					t.Fatal(err)
				}
				if entry.Row != ids.RowID(i+1) || entry.Version != ids.VersionID(i+1) || first != int64(i+1) || second != 7 || len(entry.Keys) != 2 {
					t.Fatal("physical attribute mapping or INCLUDE uniqueness changed", entry)
				}
			}
			if backing.SQLIndex.PartitionParent != f.parent.Address() || local.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
				t.Fatal("new backing lost ancestry or hijacked unrelated local index")
			}
		})
	}
}
