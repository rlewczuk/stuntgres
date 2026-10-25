package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
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
			current := catalog.CloneState(f.e.Catalog.State)
			current.SetDescriptor(local.Address(), &local)
			f.e.Catalog.PublishLocked(current)
			mapped, err := index.MapPartitionIndex(context.Background(), f.e.Catalog.Read(current), f.root, f.leaf, f.parent.SQLIndex)
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
			f.e.Mu.Lock()
			f.e.Storage.InstallRelationLocked(f.leaf.Address(), store)
			f.e.Mu.Unlock()
			oldRoot, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.leaf, local.SQLIndex, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.e.Mu.Lock()
			f.e.Storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{local.Address(): oldRoot})
			f.e.Mu.Unlock()
			beforeStore := uniquePublicationStore(store)
			beforeEntries := slices.Clone(oldRoot.Entries)
			beforeCatalog, beforeRoots := f.e.Catalog.State, maps.Clone(f.e.Storage.IndexRoots)
			captured := index.CaptureIndexStores(f.e.Storage.Relations, next)
			roots, err := core.EnginePrepareIndexRoots(f.e, &exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, current, next, captured, f.e.Storage.IndexRoots, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil)
			if f.e.Catalog.State != beforeCatalog || !maps.Equal(f.e.Storage.IndexRoots, beforeRoots) || !reflect.DeepEqual(*store, beforeStore) || !reflect.DeepEqual(oldRoot.Entries, beforeEntries) {
				t.Fatal("private index build mutated original catalog/store/backing")
			}
			if duplicate {
				var diagnostic *sqlstate.Error
				if roots != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Message != `could not create unique index "attach_built_unique"` || diagnostic.Constraint != backing.Name() {
					t.Fatal("duplicate build returned partial roots or wrong failure", roots, err)
				}
				if f.e.Catalog.State.Get(backing.Address()) != nil {
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

func partitionConstraintDescriptors(t *testing.T) partitionConstraintUnit {
	t.Helper()
	root, idx, catalogSnapshot, types := indexClausesFixture(t)
	e := engine.New()
	state := *e.Catalog.State
	state.ResetMaps()
	for a, d := range e.Catalog.State.Descriptors() {
		state.SetDescriptor(a, d)
	}
	for k, a := range e.Catalog.State.Names() {
		state.BindName(k, a)
	}
	root.ObjectName = "unit_root"
	root.RelationKind = catalog.PartitionedRelation
	keyClass, _ := sqltype.DefaultIndexClass(types[sqltype.Int4])
	comparison, err := routine.BindIndexOrdering(e.Catalog.Read(&state), root.Columns[0].Usage, keyClass)
	if err != nil {
		t.Fatal(err)
	}
	root.Partition = &catalog.PartitionDefinition{Keys: []catalog.PartitionKeyEntry{{Attribute: 10, Usage: root.Columns[0].Usage, Class: keyClass, Comparison: comparison, Label: "a"}}}
	idx.ObjectName = "unit_parent"
	idx.SQLIndex.Unique = true
	idx.SQLIndex.Valid = false
	leaf := *root
	leaf.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	leaf.ObjectName = "unit_leaf"
	leaf.Partition = nil
	leaf.RelationKind = catalog.TableRelation
	leaf.Membership = &catalog.PartitionMembership{Parent: root.Address(), DefaultBound: true}
	leaf.Columns = []catalog.Column{root.Columns[1], root.Columns[0], root.Columns[2]}
	leaf.Attributes = []int32{11, 22, 33}
	leaf.ColumnMeta.HighWater = 33
	leaf.Index = map[string]int{"b": 0, "a": 1, "p": 2}
	root.Partition.Children = []ids.ObjectAddress{leaf.Address()}
	root.Partition.DefaultChild = leaf.Address()
	for a, d := range catalogSnapshot.Descriptors() {
		// The reduced fixture uses a different engine's immutable seed.
		// Retain this engine's genuine callable and principal authority.
		if state.Get(a) == nil {
			state.SetDescriptor(a, d)
		}
	}
	state.SetDescriptor(leaf.Address(), &leaf)
	view := e.Catalog.Read(&state)
	mapped, err := index.MapPartitionIndex(context.Background(), view, root, &leaf, idx.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	mapped.Valid = true
	child := *idx
	child.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20003}
	child.ObjectName = "unit_child"
	child.SQLIndex = mapped
	child.Columns = slices.Clone(idx.Columns)
	child.Attributes = slices.Clone(idx.Attributes)
	state.SetDescriptor(child.Address(), &child)
	for _, d := range []*catalog.Relation{root, &leaf, idx, &child} {
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	e.Catalog.PublishLocked(&state)
	return partitionConstraintUnit{e: e, root: root, leaf: &leaf, parent: idx, child: &child, view: view}
}

type partitionConstraintUnit struct {
	e                         *core.Engine
	root, leaf, parent, child *catalog.Relation
	view                      catalog.Read
}
