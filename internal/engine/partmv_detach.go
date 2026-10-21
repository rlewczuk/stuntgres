package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
)

// StageDetach stages DETACH PARTITION of child from parent. Only
// descriptors of the immediate edge are replaced. Stores, logical roots,
// rowtype, stable attributes, local definitions and descendant edges are
// shared. base is the catalog the stage began from. It holds nothing.
func StageDetach(stage ddl.Stage, base catalog.Read, parent, child *catalog.Relation) error {
	if err := partition.DetachMembership(parent, child); err != nil {
		return err
	}
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) || child.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	nextParent, err := partition.WithoutChild(parent, child.Address())
	if err != nil {
		return err
	}
	nextChild := *child
	nextChild.Membership = nil
	nextChild.ObjectRevision++
	if len(child.Constraints.NotNull) != 0 {
		nextChild.Constraints.NotNull = slices.Clone(child.Constraints.NotNull)
		for i := range nextChild.Constraints.NotNull {
			nextChild.Constraints.NotNull[i].PartitionLocal = true
		}
	}
	if len(child.Constraints.Unique) != 0 {
		nextChild.Constraints.Unique = slices.Clone(child.Constraints.Unique)
		for i := range nextChild.Constraints.Unique {
			c := &nextChild.Constraints.Unique[i]
			c.Parent = 0
			c.IsLocal = true
		}
	}
	if len(child.Constraints.Exclusion) != 0 {
		nextChild.Constraints.Exclusion = slices.Clone(child.Constraints.Exclusion)
		for i := range nextChild.Constraints.Exclusion {
			nextChild.Constraints.Exclusion[i].Parent = 0
			nextChild.Constraints.Exclusion[i].IsLocal = true
		}
	}
	if err := stage.Replace(nextParent); err != nil {
		return err
	}
	if err := stage.Replace(&nextChild); err != nil {
		return err
	}
	for _, descriptor := range base.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != child.Address() || index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			continue
		}
		ancestor, ok := base.Relation(index.SQLIndex.PartitionParent)
		if !ok || ancestor.SQLIndex == nil || ancestor.SQLIndex.Parent != parent.Address() {
			return fmt.Errorf("stuntgres: invalid partition index ancestry")
		}
		if index.Revision() == ids.DefinitionRevision(^uint64(0)) {
			return fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next, definition := *index, *index.SQLIndex
		definition.PartitionParent = ids.ObjectAddress{}
		// The backing contains own identity/kind, not inheritance provenance;
		// retain it while the table constraint alone becomes local.
		next.SQLIndex = &definition
		next.ObjectRevision++
		if err := stage.Replace(&next); err != nil {
			return err
		}
	}
	// DETACH never validates/promotes an incomplete ONLY ancestor index merely
	// because removing a branch makes its coverage complete.
	return nil
}
