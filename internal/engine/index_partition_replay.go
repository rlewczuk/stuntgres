package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func partitionReplayUsingIndex(view catalog.Read, index *catalog.Relation) bool {
	table := view.MustRelation(index.SQLIndex.Parent)
	return usingIndexDump(index) && partitionReplayVirtualUniqueRoot(view, index) == nil || table.Partition == nil && index.SQLIndex.Constraint != nil && index.SQLIndex.Constraint.Kind == catalog.UniqueBackingKind && partitionPreservedReplayLocal(view, index)
}

// Delayed virtual definitions own a local constraint origin. Recursive UNIQUE
// creates its tree at declaration; incomplete EXCLUDE uses ONLY and memberships.
func partitionReplayVirtualUniqueRoot(view catalog.Read, index *catalog.Relation) *catalog.Relation {
	if index.SQLIndex == nil || index.SQLIndex.Constraint == nil {
		return nil
	}
	for index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		index = view.MustRelation(index.SQLIndex.PartitionParent)
		if index.SQLIndex.Constraint == nil {
			return nil
		}
	}
	table := view.MustRelation(index.SQLIndex.Parent)
	if table.Partition != nil && (partitionReplayOnlyExclusion(index) || partitionPreservedReplayLocal(view, index)) {
		return index
	}
	return nil
}

func partitionReplaySeparateConstraint(view catalog.Read, index *catalog.Relation) bool {
	return partitionReplayUsingIndex(view, index) || partitionReplayVirtualUniqueRoot(view, index) != nil
}

// ONLY declarations create just the local origin. Captured covered providers
// are created later by their table membership, never by index attachment.
func partitionReplayOnlyExclusion(index *catalog.Relation) bool {
	return index.SQLIndex != nil && index.SQLIndex.IsExclusion() && !index.SQLIndex.Valid && index.SQLIndex.PartitionParent == (ids.ObjectAddress{})
}
