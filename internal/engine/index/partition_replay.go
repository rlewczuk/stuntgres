package index

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func PartitionReplayUsingIndex(view catalog.Read, index *catalog.Relation) bool {
	table := view.MustRelation(index.SQLIndex.Parent)
	return UsingIndexDump(index) && PartitionReplayVirtualUniqueRoot(view, index) == nil || table.Partition == nil && index.SQLIndex.Constraint != nil && index.SQLIndex.Constraint.Kind == catalog.UniqueBackingKind && PartitionPreservedReplayLocal(view, index)
}

// Delayed virtual definitions own a local constraint origin. Recursive UNIQUE
// creates its tree at declaration; incomplete EXCLUDE uses ONLY and memberships.
func PartitionReplayVirtualUniqueRoot(view catalog.Read, index *catalog.Relation) *catalog.Relation {
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
	if table.Partition != nil && (PartitionReplayOnlyExclusion(index) || PartitionPreservedReplayLocal(view, index)) {
		return index
	}
	return nil
}

func PartitionReplaySeparateConstraint(view catalog.Read, index *catalog.Relation) bool {
	return PartitionReplayUsingIndex(view, index) || PartitionReplayVirtualUniqueRoot(view, index) != nil
}

// ONLY declarations create just the local origin. Captured covered providers
// are created later by their table membership, never by index attachment.
func PartitionReplayOnlyExclusion(index *catalog.Relation) bool {
	return index.SQLIndex != nil && index.SQLIndex.IsExclusion() && !index.SQLIndex.Valid && index.SQLIndex.PartitionParent == (ids.ObjectAddress{})
}

// Replay identity-sensitive rows through their highest HASH ancestor; RANGE-only
// trees preserve physical destinations. Parent insertion checks every ancestor.
func PartitionHashReplayTarget(catalog catalog.Read, d *catalog.Relation) *catalog.Relation {
	target := d
	for child := d; child.Membership != nil; {
		parent := catalog.MustRelation(child.Membership.Parent)
		if parent.Partition.Strategy == parser.PartitionHash {
			target = parent
		}
		child = parent
	}
	return target
}

func PartitionDumpIndexBacking(view catalog.Read, index, table ids.ObjectAddress) *catalog.Relation {
	for _, descriptor := range view.Descriptors() {
		candidate, ok := descriptor.(*catalog.Relation)
		if ok && candidate.SQLIndex != nil && candidate.SQLIndex.Parent == table && candidate.SQLIndex.PartitionParent == index {
			return candidate
		}
	}
	return nil
}

func PartitionIndexPreservedInvalid(view catalog.Read, index *catalog.Relation) bool {
	table, ok := view.Relation(index.SQLIndex.Parent)
	return ok && table.Partition != nil && !index.SQLIndex.Valid && PartitionIndexCoverageComplete(view, index)
}

func PartitionReplayNameCompare(view catalog.Read, a, b *catalog.Relation) int {
	if order := strings.Compare(view.Get(a.Namespace()).Name(), view.Get(b.Namespace()).Name()); order != 0 {
		return order
	}
	return strings.Compare(a.Name(), b.Name())
}

func PartitionPreservedReplayChild(view catalog.Read, table *catalog.Relation) *catalog.Relation {
	var chosen *catalog.Relation
	for _, address := range table.Partition.Children {
		child := view.MustRelation(address)
		if chosen == nil || PartitionReplayNameCompare(view, child, chosen) < 0 {
			chosen = child
		}
	}
	return chosen
}

// Unattached local unique providers must not steal a saved attached backing
// during the one table reattachment that preserves complete-invalid validity.
func PartitionPreservedReplayLocal(view catalog.Read, index *catalog.Relation) bool {
	if !index.SQLIndex.Unique || index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		return false
	}
	table := view.MustRelation(index.SQLIndex.Parent)
	if table.Membership == nil {
		return false
	}
	parent := view.MustRelation(table.Membership.Parent)
	chosen := PartitionPreservedReplayChild(view, parent)
	if chosen == nil || chosen.Address() != table.Address() {
		return false
	}
	for _, descriptor := range view.Descriptors() {
		candidate, ok := descriptor.(*catalog.Relation)
		if ok && candidate.SQLIndex != nil && candidate.SQLIndex.Parent == parent.Address() && PartitionIndexPreservedInvalid(view, candidate) {
			saved := PartitionDumpIndexBacking(view, candidate.Address(), table.Address())
			match, err := PartitionIndexEquivalent(context.Background(), view, table, saved.SQLIndex, index.SQLIndex)
			if err == nil && match {
				return true
			}
		}
	}
	return false
}
