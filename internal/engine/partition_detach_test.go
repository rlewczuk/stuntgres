package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

type partitionDetachUnit struct {
	e                        *core.Engine
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
		return e.Catalog.State.MustRelation(d.Address())
	}
	return partitionDetachUnit{e, get(parent), get(child), get(def), get(leaf)}
}
