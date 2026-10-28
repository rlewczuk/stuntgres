package partition

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func DetachMembership(parent, child *catalog.Relation) error {
	if child.Membership == nil || child.Membership.Parent != parent.Address() || !slices.Contains(parent.Partition.Children, child.Address()) {
		return sqlError("42P01", fmt.Sprintf(`relation "%s" is not a partition of relation "%s"`, child.Name(), parent.Name()))
	}
	return nil
}

func DetachKindError(view catalog.Read, d *catalog.Relation, opening bool) error {
	kind := "relations"
	switch d.RelationKind {
	case catalog.TableRelation:
		kind = "tables"
	case catalog.ViewRelation:
		kind = "views"
	case catalog.MaterializedViewRelation:
		kind = "materialized views"
	case catalog.SequenceRelation:
		kind = "sequences"
	case catalog.CompositeRelation:
		kind = "composite types"
	case catalog.IndexRelation:
		kind = "indexes"
		if table, ok := view.Relation(d.SQLIndex.Parent); ok && table.Partition != nil {
			kind = "partitioned indexes"
		}
	}
	message := fmt.Sprintf(`ALTER action DETACH PARTITION cannot be performed on relation "%s"`, d.Name())
	if opening {
		message = fmt.Sprintf(`cannot open relation "%s"`, d.Name())
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: message, Detail: "This operation is not supported for " + kind + "."}
}

// StageDetach stages DETACH PARTITION of child from parent. Only
// descriptors of the immediate edge are replaced. Stores, logical roots,
// rowtype, stable attributes, local definitions and descendant edges are
// shared. base is the catalog the stage began from. It holds nothing.
func StageDetach(stage ddl.Stage, base catalog.Read, parent, child *catalog.Relation) error {
	if err := DetachMembership(parent, child); err != nil {
		return err
	}
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) || child.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	nextParent, err := WithoutChild(parent, child.Address())
	if err != nil {
		return err
	}
	nextChild := *child
	nextChild.Membership = nil
	nextChild.ObjectRevision++
	nextChild.History.DetachPredecessor = nil
	nextChild.History.DetachPublication = 0
	nextChild.History.DetachRoot = ids.ObjectAddress{}
	if len(child.Constraints.NotNull) != 0 {
		nextChild.Constraints.NotNull = slices.Clone(child.Constraints.NotNull)
		for i := range nextChild.Constraints.NotNull {
			nextChild.Constraints.NotNull[i].PartitionOrigin = nil
		}
	}
	nextChild.Constraints.Check = slices.Clone(child.Constraints.Check)
	for i := range nextChild.Constraints.Check {
		nextChild.Constraints.Check[i].PartitionOrigin = nil
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
		if ok && index.Address() != child.Address() && index.History.DetachRoot == child.Address() {
			next := *index
			next.History.DetachPredecessor = nil
			next.History.DetachPublication = 0
			next.History.DetachRoot = ids.ObjectAddress{}
			if err := stage.Replace(&next); err != nil {
				return err
			}
		}
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
