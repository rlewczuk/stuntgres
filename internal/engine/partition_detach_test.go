package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type partitionDetachUnit struct {
	e                        *Engine
	parent, child, def, leaf *catalog.Relation
}

func partitionDetachUnitTree(t *testing.T, nested bool) partitionDetachUnit {
	t.Helper()
	e := New()
	parent := partitionAttachUnitRoot(t, e, "detach_unit_parent", 46500)
	child := inheritanceUnitRelation(e, "detach_unit_child", 46501, "k")
	child.RelationKind = catalog.TableRelation
	if nested {
		child = partitionAttachUnitRoot(t, e, child.Name(), child.Address().ObjectID)
	}
	child.Membership = partitionAttachUnitRange(t, parent, 1, 3)
	def := inheritanceUnitRelation(e, "detach_unit_default", 46502, "k")
	def.RelationKind = catalog.TableRelation
	def.Membership = &catalog.PartitionMembership{Parent: parent.Address(), DefaultBound: true}
	parent.Partition.Children = []ids.ObjectAddress{child.Address(), def.Address()}
	parent.Partition.DefaultChild = def.Address()
	relations := []*catalog.Relation{parent, child, def}
	leaf := child
	if nested {
		leaf = inheritanceUnitRelation(e, "detach_unit_leaf", 46503, "k")
		leaf.RelationKind = catalog.TableRelation
		leaf.Membership = &catalog.PartitionMembership{Parent: child.Address(), DefaultBound: true}
		child.Partition.Children = []ids.ObjectAddress{leaf.Address()}
		child.Partition.DefaultChild = leaf.Address()
		relations = append(relations, leaf)
	}
	partitionAttachUnitInstall(t, e, relations...)
	get := func(d *catalog.Relation) *catalog.Relation {
		return e.catalog.state.MustRelation(d.Address())
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
			stores := maps.Clone(f.e.storage.Relations)
			stage := f.e.BeginCatalogStage(f.e.CatalogRead())
			defer f.e.DiscardCatalogStage(stage)
			if err := f.e.stagePartitionDetach(stage, f.parent, child); err != nil {
				t.Fatal(err)
			}
			view := f.e.stageRead(stage)
			p := view.MustRelation(f.parent.Address())
			c := view.MustRelation(child.Address())
			if p == f.parent || c == child || c.Membership != nil || slices.Contains(p.Partition.Children, child.Address()) || c.Address() != child.Address() || c.RowType != child.RowType || c.Partition != child.Partition {
				t.Fatal("immediate graph/identity/subtree changed incorrectly")
			}
			if &c.Columns[0] != &child.Columns[0] || &c.Attributes[0] != &child.Attributes[0] || c.Index == nil {
				t.Fatal("stable physical shape was copied or lost")
			}
			if p.Revision() != f.parent.Revision()+1 || c.Revision() != child.Revision()+1 {
				t.Fatal("revision invalidation missing")
			}
			if (p.Partition.DefaultChild == (ids.ObjectAddress{})) != (kind == "default") {
				t.Fatal("wrong DEFAULT pointer")
			}
			if f.e.catalog.state != before || !maps.Equal(f.e.storage.Relations, stores) || len(f.parent.Partition.Children) != 2 || child.Membership == nil {
				t.Fatal("private stage mutated published graph/store")
			}
			if kind == "nested" && view.Get(f.leaf.Address()) != f.leaf {
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
			f.child.SQLIndex.PartitionParent = f.parent.Address()
			f.parent.SQLIndex.Valid = valid
			f.parent.SQLIndex.Include = nil
			f.child.SQLIndex.Include = nil
			// Build genuine inline-UNIQUE tuple descriptors, not the covering
			// index fixture's extra payload or nondefault key ordering.
			for _, index := range [2]*catalog.Relation{f.parent, f.child} {
				index.Columns = index.Columns[:2]
				index.Attributes = index.Attributes[:2]
				index.ColumnMeta.HighWater = 2
				index.Index = map[string]int{"a": 0, "b": 1}
				index.SQLIndex.Keys[1].Desc = false
				index.SQLIndex.Keys[1].NullsFirst = false
			}
			rootKey := catalog.UniqueConstraint{OID: 46600, Name: f.parent.Name(), Index: f.parent.Address(), Attributes: []int32{10, 20}, IsLocal: true}
			childKey := catalog.UniqueConstraint{OID: 46601, Name: f.child.Name(), Index: f.child.Address(), Attributes: []int32{22, 11}, Parent: rootKey.OID}
			f.root.Constraints.Unique = []catalog.UniqueConstraint{rootKey}
			f.leaf.Constraints.Unique = []catalog.UniqueConstraint{childKey}
			f.parent.SQLIndex.Constraint = uniqueBacking(&rootKey)
			f.child.SQLIndex.Constraint = uniqueBacking(&childKey)
			f.leaf.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 46602, Name: "own_nn", Attribute: 22}}
			stage := f.e.BeginCatalogStage(f.view)
			defer f.e.DiscardCatalogStage(stage)
			if err := f.e.stagePartitionDetach(stage, f.root, f.leaf); err != nil {
				t.Fatal(err)
			}
			view := f.e.stageRead(stage)
			index := view.MustRelation(f.child.Address())
			table := view.MustRelation(f.leaf.Address())
			if index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || index.SQLIndex.Constraint != f.child.SQLIndex.Constraint || index.SQLIndex.Valid != f.child.SQLIndex.Valid || table.Constraints.Unique[0].Parent != 0 || !table.Constraints.Unique[0].IsLocal || !table.Constraints.NotNull[0].PartitionLocal {
				t.Fatal("top edge/provenance/backing identity wrong")
			}
			if table.Constraints.Unique[0].OID != childKey.OID || table.Constraints.NotNull[0].OID != 46602 || f.leaf.Constraints.Unique[0].Parent != rootKey.OID || f.child.SQLIndex.PartitionParent != f.parent.Address() {
				t.Fatal("own identity or predecessor changed")
			}
			if view.Get(f.parent.Address()) != f.parent || f.parent.SQLIndex.Valid != valid {
				t.Fatal("detach promoted or replaced former parent index")
			}
			if err := validatePartitionIndex(view, f.parent, view.MustRelation(f.root.Address())); err != nil {
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
					stage.authorizeLocked(auth, op.Session)
				}
				f.e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				authority = op.Session
			}
			prepared, err := f.e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			if authority != nil {
				authority.Mu.Lock()
				authority.Authorization.revision++
				authority.Mu.Unlock()
			}
			if veto == "revision" {
				next := catalog.CloneState(f.e.catalog.state)
				p := *f.parent
				p.ObjectRevision++
				next.SetDescriptor(p.Address(), &p)
				f.e.catalog.state = next
			}
			before := f.e.catalog.state
			stores, roots := maps.Clone(f.e.storage.Relations), maps.Clone(f.e.storage.IndexRoots)
			if veto == "cancel" {
				cancel()
			}
			_, err = f.e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if veto == "complete_then_cancel" {
				if err != nil {
					t.Fatal(err)
				}
				cancel()
				if f.e.catalog.state.MustRelation(f.child.Address()).Membership != nil || !maps.Equal(stores, f.e.storage.Relations) {
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
			if f.e.catalog.state != before || !maps.Equal(stores, f.e.storage.Relations) || !maps.Equal(roots, f.e.storage.IndexRoots) {
				t.Fatal("veto partially published")
			}
		})
	}
	// Exhaustion must not mutate any live descriptor or allocate a new identity.
	f := partitionConstraintDescriptors(t)
	f.child.SQLIndex.PartitionParent = f.parent.Address()
	f.child.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	if err := f.e.stagePartitionDetach(stage, f.root, f.leaf); err == nil || f.leaf.Membership == nil || len(f.root.Partition.Children) != 1 {
		t.Fatal("exhaustion changed original graph", err)
	}
}

// A ready-invalid virtual descriptor never needs a physical logical root, even
// when no members remain and coverage is vacuously complete.
func TestPartitionDetachVirtualRoots(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	f.child.SQLIndex.PartitionParent = f.parent.Address()
	if err := f.e.stagePartitionDetach(stage, f.root, f.leaf); err != nil {
		t.Fatal(err)
	}
	next := f.e.stageRead(stage)
	next.State().RemoveDescriptor(f.child.Address())
	next.State().RemoveDescriptor(f.leaf.Address())
	roots, err := f.e.prepareIndexPublication(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: next}}, catalog.NewState(0), next.State(), nil, nil, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots[f.parent.Address()] != nil || f.parent.SQLIndex.Valid {
		t.Fatal("virtual invalid-complete required root or promotion", err)
	}
}
