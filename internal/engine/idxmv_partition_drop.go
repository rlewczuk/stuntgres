package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// StagePartitionIndexDrop stages the removal of idx and of its backing
// constraint on the table. It runs without Engine.mu; the removal takes it.
func StagePartitionIndexDrop(stage index.DropStage, idx *catalog.Relation) error {
	if idx.SQLIndex.Constraint != nil {
		view := stage.StageRead()
		table := view.MustRelation(idx.SQLIndex.Parent)
		next := *table
		next.Constraints.Unique = slices.Clone(table.Constraints.Unique)
		next.ObjectRevision++
		next.Constraints.Unique = slices.DeleteFunc(next.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Index == idx.Address() })
		next.Constraints.Exclusion = slices.DeleteFunc(slices.Clone(table.Constraints.Exclusion), func(c catalog.ExclusionConstraint) bool { return c.Index == idx.Address() })
		if err := index.StagePartitionDescriptor(stage, &next); err != nil {
			return err
		}
	}
	return stage.RemoveRelation(idx.Address())
}

// StagePartitionConstraintDrop stages the removal of every partition's
// constraint inherited from oid, and of their indexes, depth first.
func StagePartitionConstraintDrop(stage index.DropStage, oid ids.OID) error {
	view := stage.StageRead()
	for _, descriptor := range view.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, c := range table.Constraints.Unique {
			if c.Parent != oid {
				continue
			}
			if err := StagePartitionConstraintDrop(stage, c.OID); err != nil {
				return err
			}
			if err := StagePartitionIndexDrop(stage, view.MustRelation(c.Index)); err != nil {
				return err
			}
		}
		for _, c := range table.Constraints.Exclusion {
			if c.Parent != oid {
				continue
			}
			if err := StagePartitionConstraintDrop(stage, c.OID); err != nil {
				return err
			}
			if err := StagePartitionIndexDrop(stage, view.MustRelation(c.Index)); err != nil {
				return err
			}
		}
	}
	return nil
}
