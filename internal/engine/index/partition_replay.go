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
	if PartitionReplayFreshOrigin(view, index) != nil {
		return nil
	}
	for index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		if index.SQLIndex.IsExclusion() {
			table := view.MustRelation(index.SQLIndex.Parent)
			if c := ExclusionDefinition(table, index); c != nil && c.NoInherit {
				break
			}
		}
		index = view.MustRelation(index.SQLIndex.PartitionParent)
		if index.SQLIndex.Constraint == nil {
			return nil
		}
	}
	table := view.MustRelation(index.SQLIndex.Parent)
	if table.Partition != nil && index.SQLIndex.IsExclusion() {
		for _, address := range table.Partition.Children {
			child := PartitionDumpIndexBacking(view, index.Address(), address)
			if child != nil {
				owner := view.MustRelation(address)
				if c := ExclusionDefinition(owner, child); c != nil && c.NoInherit {
					return index
				}
			}
		}
	}
	if table.Partition != nil && (PartitionReplayOnlyExclusion(view, index) || PartitionPreservedReplayLocal(view, index) || PartitionReplayDeferredExclusion(view, index)) {
		return index
	}
	return nil
}

func PartitionReplaySeparateConstraint(view catalog.Read, index *catalog.Relation) bool {
	return PartitionReplayUsingIndex(view, index) || PartitionReplayVirtualUniqueRoot(view, index) != nil || PartitionReplayDeferredExclusion(view, index)
}

// ONLY retains a declaration origin's direct gaps or complete-invalid state.
// A matching invalid subtree instead needs recursive ADD after its declaration:
// table ATTACH would skip that candidate and silently create a different tree.
func PartitionReplayOnlyExclusion(view catalog.Read, index *catalog.Relation) bool {
	if index.SQLIndex == nil || !index.SQLIndex.IsExclusion() {
		return false
	}
	table := view.MustRelation(index.SQLIndex.Parent)
	if table.Partition != nil {
		for _, address := range table.Partition.Children {
			if child := PartitionDumpIndexBacking(view, index.Address(), address); child != nil && PartitionReplayCompetingExclusion(view, child) {
				return true
			}
		}
	}
	if index.SQLIndex.Valid {
		return false
	}
	c := ExclusionDefinition(table, index)
	if table.Partition == nil || c == nil || c.Parent != 0 && !c.NoInherit {
		return false
	}
	invalidChild := false
	for _, address := range table.Partition.Children {
		child := PartitionDumpIndexBacking(view, index.Address(), address)
		if child == nil {
			return true
		}
		invalidChild = invalidChild || !child.SQLIndex.Valid
	}
	return !invalidChild
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

// PartitionReplayFreshOrigin finds the detached, freshly inherited declaration
// that must be created by a temporary parent rather than a local declaration.
// Originally local adopted providers stop this search: they own their own tree.
func PartitionReplayFreshOrigin(view catalog.Read, idx *catalog.Relation) *catalog.Relation {
	for idx.SQLIndex != nil && idx.SQLIndex.IsExclusion() {
		table := view.MustRelation(idx.SQLIndex.Parent)
		c := ExclusionDefinition(table, idx)
		if c == nil || c.NoInherit {
			return nil
		}
		if c.Parent == 0 || PartitionReplayExplicitExclusion(view, idx) {
			return idx
		}
		if idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			return nil
		}
		idx = view.MustRelation(idx.SQLIndex.PartitionParent)
	}
	return nil
}

// Equivalent independent locals follow the inherited provider's real creator,
// whether ordinary membership or scratch cloning. Otherwise constructor-time
// adoption would erase the captured distinction between declaration origins.
func PartitionReplayDeferredExclusion(view catalog.Read, idx *catalog.Relation) bool {
	if idx.SQLIndex == nil || !idx.SQLIndex.IsExclusion() {
		return false
	}
	table := view.MustRelation(idx.SQLIndex.Parent)
	c := ExclusionDefinition(table, idx)
	if c == nil || !c.NoInherit || PartitionReplayCompetingExclusion(view, idx) {
		return false
	}
	for _, other := range table.Constraints.Exclusion {
		inherited := c.Parent == 0 && !other.NoInherit && other.Parent != 0
		origin := PartitionReplayFreshOrigin(view, view.MustRelation(other.Index))
		if !inherited && (origin == nil || origin.Address() != other.Index) {
			continue
		}
		match, err := PartitionIndexEquivalent(context.Background(), view, table, idx.SQLIndex, view.MustRelation(other.Index).SQLIndex)
		if err == nil && match {
			return true
		}
	}
	return false
}

// An invalid child cannot be adopted by TABLE ATTACH. When the parent retains
// its own gaps with ONLY, an explicit index attachment supplies the crossing
// edge after table membership and both captured provider definitions exist.
func PartitionReplayExplicitExclusion(view catalog.Read, idx *catalog.Relation) bool {
	if idx.SQLIndex == nil || !idx.SQLIndex.IsExclusion() || idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
		return false
	}
	if PartitionReplayCompetingExclusion(view, idx) {
		return true
	}
	parent := view.MustRelation(idx.SQLIndex.PartitionParent)
	return !idx.SQLIndex.Valid && PartitionReplayOnlyExclusion(view, parent)
}

// A captured adopted origin can coexist with an equivalent independent false
// provider. Recreating that provider first must not redirect the chosen edge.
// This policy uses actual owning slices and the shared comparator, never OIDs.
func PartitionReplayCompetingExclusion(view catalog.Read, idx *catalog.Relation) bool {
	if idx.SQLIndex == nil || !idx.SQLIndex.IsExclusion() || idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
		return false
	}
	table := view.MustRelation(idx.SQLIndex.Parent)
	c := ExclusionDefinition(table, idx)
	if c == nil || !c.NoInherit || c.Parent == 0 {
		return false
	}
	for i := range table.Constraints.Exclusion {
		other := &table.Constraints.Exclusion[i]
		if other.Parent != 0 || other.NoInherit {
			continue
		}
		match, err := PartitionIndexEquivalent(context.Background(), view, table, idx.SQLIndex, view.MustRelation(other.Index).SQLIndex)
		if err == nil && match {
			return true
		}
	}
	return false
}
