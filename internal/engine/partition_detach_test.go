package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type partitionDetachUnit struct {
	e                        *Engine
	parent, child, def, leaf *relationDescriptor
}

func partitionDetachUnitTree(t *testing.T, nested bool) partitionDetachUnit {
	t.Helper()
	e := New()
	parent := partitionAttachUnitRoot(t, e, "detach_unit_parent", 46500)
	child := inheritanceUnitRelation(e, "detach_unit_child", 46501, "k")
	child.relationKind = tableRelation
	if nested {
		child = partitionAttachUnitRoot(t, e, child.name, child.address.ObjectID)
	}
	child.membership = partitionAttachUnitRange(t, parent, 1, 3)
	def := inheritanceUnitRelation(e, "detach_unit_default", 46502, "k")
	def.relationKind = tableRelation
	def.membership = &partitionMembership{parent: parent.address, defaultBound: true}
	parent.partition.children = []ids.ObjectAddress{child.address, def.address}
	parent.partition.defaultChild = def.address
	relations := []*relationDescriptor{parent, child, def}
	leaf := child
	if nested {
		leaf = inheritanceUnitRelation(e, "detach_unit_leaf", 46503, "k")
		leaf.relationKind = tableRelation
		leaf.membership = &partitionMembership{parent: child.address, defaultBound: true}
		child.partition.children = []ids.ObjectAddress{leaf.address}
		child.partition.defaultChild = leaf.address
		relations = append(relations, leaf)
	}
	partitionAttachUnitInstall(t, e, relations...)
	get := func(d *relationDescriptor) *relationDescriptor {
		return e.catalog.state.descriptors[d.address].(*relationDescriptor)
	}
	return partitionDetachUnit{e, get(parent), get(child), get(def), get(leaf)}
}

func TestPartitionDetachDescriptor(t *testing.T) {
	for _, kind := range []string{"leaf", "default", "nested"} {
		t.Run(kind, func(t *testing.T) {
			f := partitionDetachUnitTree(t, kind == "nested")
			child := f.child
			if kind == "default" {
				child = f.def
			}
			before := f.e.catalog.state
			stores := maps.Clone(f.e.storage)
			stage := f.e.BeginCatalogStage(f.e.CatalogRead())
			defer f.e.DiscardCatalogStage(stage)
			if err := f.e.stagePartitionDetach(stage, f.parent, child); err != nil {
				t.Fatal(err)
			}
			view := f.e.stageRead(stage)
			p := view.state.descriptors[f.parent.address].(*relationDescriptor)
			c := view.state.descriptors[child.address].(*relationDescriptor)
			if p == f.parent || c == child || c.membership != nil || slices.Contains(p.partition.children, child.address) || c.address != child.address || c.rowType != child.rowType || c.partition != child.partition {
				t.Fatal("immediate graph/identity/subtree changed incorrectly")
			}
			if &c.columns[0] != &child.columns[0] || &c.attributes[0] != &child.attributes[0] || c.index == nil {
				t.Fatal("stable physical shape was copied or lost")
			}
			if p.revision != f.parent.revision+1 || c.revision != child.revision+1 {
				t.Fatal("revision invalidation missing")
			}
			if (p.partition.defaultChild == (ids.ObjectAddress{})) != (kind == "default") {
				t.Fatal("wrong DEFAULT pointer")
			}
			if f.e.catalog.state != before || !maps.Equal(f.e.storage, stores) || len(f.parent.partition.children) != 2 || child.membership == nil {
				t.Fatal("private stage mutated published graph/store")
			}
			if kind == "nested" && view.state.descriptors[f.leaf.address] != f.leaf {
				t.Fatal("subordinate descriptor replaced")
			}
			if _, err := f.e.ValidateCatalogStage(context.Background(), stage); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPartitionDetachDependencies(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid_complete", true: "valid"}[valid], func(t *testing.T) {
			f := partitionConstraintDescriptors(t)
			f.child.sqlIndex.partitionParent = f.parent.address
			f.parent.sqlIndex.valid = valid
			f.parent.sqlIndex.include = nil
			f.child.sqlIndex.include = nil
			// Build genuine inline-UNIQUE tuple descriptors, not the covering
			// index fixture's extra payload or nondefault key ordering.
			for _, index := range [2]*relationDescriptor{f.parent, f.child} {
				index.columns = index.columns[:2]
				index.attributes = index.attributes[:2]
				index.attributeHighWater = 2
				index.index = map[string]int{"a": 0, "b": 1}
				index.sqlIndex.keys[1].desc = false
				index.sqlIndex.keys[1].nullsFirst = false
			}
			rootKey := uniqueConstraint{OID: 46600, Name: f.parent.name, Index: f.parent.address, Attributes: []int32{10, 20}, IsLocal: true}
			childKey := uniqueConstraint{OID: 46601, Name: f.child.name, Index: f.child.address, Attributes: []int32{22, 11}, Parent: rootKey.OID}
			f.root.uniqueConstraints = []uniqueConstraint{rootKey}
			f.leaf.uniqueConstraints = []uniqueConstraint{childKey}
			f.parent.sqlIndex.constraint = uniqueBacking(&rootKey)
			f.child.sqlIndex.constraint = uniqueBacking(&childKey)
			f.leaf.notNullConstraints = []notNullConstraint{{OID: 46602, Name: "own_nn", Attribute: 22}}
			stage := f.e.BeginCatalogStage(f.view)
			defer f.e.DiscardCatalogStage(stage)
			if err := f.e.stagePartitionDetach(stage, f.root, f.leaf); err != nil {
				t.Fatal(err)
			}
			view := f.e.stageRead(stage)
			index := view.state.descriptors[f.child.address].(*relationDescriptor)
			table := view.state.descriptors[f.leaf.address].(*relationDescriptor)
			if index.sqlIndex.partitionParent != (ids.ObjectAddress{}) || index.sqlIndex.constraint != f.child.sqlIndex.constraint || index.sqlIndex.valid != f.child.sqlIndex.valid || table.uniqueConstraints[0].Parent != 0 || !table.uniqueConstraints[0].IsLocal || !table.notNullConstraints[0].partitionLocal {
				t.Fatal("top edge/provenance/backing identity wrong")
			}
			if table.uniqueConstraints[0].OID != childKey.OID || table.notNullConstraints[0].OID != 46602 || f.leaf.uniqueConstraints[0].Parent != rootKey.OID || f.child.sqlIndex.partitionParent != f.parent.address {
				t.Fatal("own identity or predecessor changed")
			}
			if view.state.descriptors[f.parent.address] != f.parent || f.parent.sqlIndex.valid != valid {
				t.Fatal("detach promoted or replaced former parent index")
			}
			if err := validatePartitionIndex(view, f.parent, view.state.descriptors[f.root.address].(*relationDescriptor)); err != nil {
				t.Fatal("invalid-complete must be admitted", err)
			}
			if err := validateIndexDefinition(view, index); err != nil {
				t.Fatal(err)
			}
			if !sameIndexKeySemantics(f.child, index) {
				t.Fatal("metadata-only detach would rebuild physical keys")
			}
		})
	}
}

func TestPartitionDetachPublication(t *testing.T) {
	for _, veto := range []string{"cancel", "revision", "authority", "complete_then_cancel"} {
		t.Run(veto, func(t *testing.T) {
			f := partitionDetachUnitTree(t, false)
			stage := f.e.BeginCatalogStage(f.e.CatalogRead())
			defer f.e.DiscardCatalogStage(stage)
			if err := f.e.stagePartitionDetach(stage, f.parent, f.child); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var authority *Session
			if veto == "authority" {
				op, done := roleOperation(t, f.e, "postgres")
				defer done()
				f.e.mu.Lock()
				auth, err := f.e.authorizationLocked(op)
				if err == nil {
					stage.authorizeLocked(auth, op.session)
				}
				f.e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				authority = op.session
			}
			prepared, err := f.e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			if authority != nil {
				authority.mu.Lock()
				authority.authorization.revision++
				authority.mu.Unlock()
			}
			if veto == "revision" {
				next := cloneCatalog(f.e.catalog.state)
				p := *f.parent
				p.revision++
				next.descriptors[p.address] = &p
				f.e.catalog.state = next
			}
			before := f.e.catalog.state
			stores, roots := maps.Clone(f.e.storage), maps.Clone(f.e.indexRoots)
			if veto == "cancel" {
				cancel()
			}
			_, err = f.e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
			if veto == "complete_then_cancel" {
				if err != nil {
					t.Fatal(err)
				}
				cancel()
				if f.e.catalog.state.descriptors[f.child.address].(*relationDescriptor).membership != nil || !maps.Equal(stores, f.e.storage) {
					t.Fatal("completed metadata publication lost store or attachment removal")
				}
				return
			}
			if err == nil {
				t.Fatal("publication veto did not reject")
			}
			if veto == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if f.e.catalog.state != before || !maps.Equal(stores, f.e.storage) || !maps.Equal(roots, f.e.indexRoots) {
				t.Fatal("veto partially published")
			}
		})
	}
	// Exhaustion must not mutate any live descriptor or allocate a new identity.
	f := partitionConstraintDescriptors(t)
	f.child.sqlIndex.partitionParent = f.parent.address
	f.child.revision = ids.DefinitionRevision(^uint64(0))
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	if err := f.e.stagePartitionDetach(stage, f.root, f.leaf); err == nil || f.leaf.membership == nil || len(f.root.partition.children) != 1 {
		t.Fatal("exhaustion changed original graph", err)
	}
}

// A ready-invalid virtual descriptor never needs a physical logical root, even
// when no members remain and coverage is vacuously complete.
func TestPartitionDetachVirtualRoots(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	f.child.sqlIndex.partitionParent = f.parent.address
	if err := f.e.stagePartitionDetach(stage, f.root, f.leaf); err != nil {
		t.Fatal(err)
	}
	next := f.e.stageRead(stage)
	delete(next.state.descriptors, f.child.address)
	delete(next.state.descriptors, f.leaf.address)
	roots, err := f.e.prepareIndexPublication(&EvalContext{Context: context.Background(), catalog: next}, &catalogState{descriptors: map[ids.ObjectAddress]Descriptor{}}, next.state, nil, nil, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots[f.parent.address] != nil || f.parent.sqlIndex.valid {
		t.Fatal("virtual invalid-complete required root or promotion", err)
	}
}
