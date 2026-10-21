package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// PartitionIndexValidity recomputes the validity of every partitioned index
// in t. Only explicit ATTACH INDEX can promote that index (promote) and its
// ancestors; table edge changes preserve invalidity even when coverage
// becomes complete. Each pass resolves another level; the catalog validator
// rejects cycles.
func PartitionIndexValidity(t index.ValidityTarget, promote ids.ObjectAddress) error {
	for range t.DescriptorCount() {
		changed := false
		for address := range t.Descriptors() {
			if t.Removed(address) {
				continue
			}
			descriptor := t.LiveDescriptor(address)
			idx, ok := descriptor.(*catalog.Relation)
			if !ok || idx.SQLIndex == nil {
				continue
			}
			table, ok := t.LiveDescriptor(idx.SQLIndex.Parent).(*catalog.Relation)
			if !ok || table.Partition == nil {
				continue
			}
			valid := index.CoverageComplete(t, idx)
			if valid && !idx.SQLIndex.Valid && !PartitionIndexPromotionPath(t, promote, idx.Address()) {
				continue
			}
			if idx.SQLIndex.Valid == valid {
				continue
			}
			if idx.Revision() == ids.DefinitionRevision(^uint64(0)) {
				return fmt.Errorf("stuntgres: definition revision exhausted")
			}
			next, definition := *idx, *idx.SQLIndex
			definition.Valid = valid
			next.SQLIndex = &definition
			next.ObjectRevision++
			if err := t.ReplacePartitionDescriptor(&next); err != nil {
				return err
			}
			changed = true
		}
		if !changed {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: cyclic partition index hierarchy")
}

// PartitionIndexPromotionPath reports whether target is start or one of its
// partitioned-index ancestors.
func PartitionIndexPromotionPath(t index.ValidityTarget, start, target ids.ObjectAddress) bool {
	for remaining := t.DescriptorCount(); start != (ids.ObjectAddress{}) && remaining > 0; remaining-- {
		if start == target {
			return true
		}
		idx, ok := t.LiveDescriptor(start).(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			return false
		}
		start = idx.SQLIndex.PartitionParent
	}
	return false
}

// StagePartitionIndexAttach stages ALTER INDEX parent ATTACH PARTITION child:
// the partition link, the inherited backing constraint, then the validity
// pass that may promote parent.
func StagePartitionIndexAttach(h index.Host, parent, child *catalog.Relation) error {
	view := h.StageRead()
	if err := index.PartitionIndexAttachCheck(h.Context(), view, parent, child); err != nil {
		return err
	}
	if child.SQLIndex.PartitionParent != parent.Address() {
		next, definition := *child, *child.SQLIndex
		definition.PartitionParent = parent.Address()
		next.SQLIndex = &definition
		next.ObjectRevision++
		if err := h.Stage().Replace(&next); err != nil {
			return err
		}
		if parent.SQLIndex.Constraint != nil {
			table := view.MustRelation(child.SQLIndex.Parent)
			changed := *table
			changed.Constraints.Unique = slices.Clone(table.Constraints.Unique)
			changed.ObjectRevision++
			for i := range changed.Constraints.Unique {
				c := &changed.Constraints.Unique[i]
				if c.Index == child.Address() {
					c.Parent = parent.SQLIndex.Constraint.OID
					c.IsLocal = false
				}
			}
			if err := h.Stage().Replace(&changed); err != nil {
				return err
			}
		}
	}
	return PartitionIndexValidity(h.ValidityTarget(), parent.Address())
}
