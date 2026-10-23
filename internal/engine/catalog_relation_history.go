package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// markSubclassHistoryLocked sets the sticky HasSubclasses flag on every parent
// of a staged child. A staged parent is completed in place; a base parent is
// staged through replace at a fresh revision from revision. The root passes
// its allocator and CatalogStage.Replace, which also synchronizes the row
// type, and calls it under Engine.mu.
func (c *CatalogStage) markSubclassHistoryLocked(revision func() (ids.DefinitionRevision, error), replace func(catalog.Descriptor) error) error {
	for _, descriptor := range c.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if err := catalog.VisitSubclassParents(child, func(address ids.ObjectAddress) error {
			descriptor, staged := c.Find(address)
			if !staged {
				descriptor = c.Base.Get(address)
			}
			parent, ok := descriptor.(*catalog.Relation)
			if !ok {
				return fmt.Errorf("stuntgres: invalid subclass parent")
			}
			if parent.History.HasSubclasses {
				return nil
			}
			next := *parent
			next.History.HasSubclasses = true
			if staged {
				c.UpdateDescriptor(&next)
				return nil
			}
			stamp, err := revision()
			if err != nil {
				return err
			}
			next.ObjectRevision = stamp
			next.History.SubclassPredecessor, next.History.SubclassPublication = parent, stamp
			return replace(&next)
		}); err != nil {
			return err
		}
	}
	return nil
}

// markIndexHistoryLocked sets the sticky HasIndexes flag on the parent of every
// staged index; revision and replace are as for markSubclassHistoryLocked.
// Index publication and parent history are one immutable catalog candidate.
// DROP deliberately never clears this flag (PostgreSQL18 pg_class lazy flags).
func (c *CatalogStage) markIndexHistoryLocked(revision func() (ids.DefinitionRevision, error), replace func(catalog.Descriptor) error) error {
	for _, descriptor := range c.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		address := index.SQLIndex.Parent
		descriptor, staged := c.Find(address)
		if !staged {
			descriptor = c.Base.Get(address)
		}
		parent, ok := descriptor.(*catalog.Relation)
		if !ok || (parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.MaterializedViewRelation && parent.RelationKind != catalog.PartitionedRelation) {
			return fmt.Errorf("stuntgres: invalid index parent")
		}
		if parent.History.HasIndexes {
			continue
		}
		next := *parent
		next.History.HasIndexes = true
		if staged {
			// Preserve the complete already-staged parent and its revision/rowtype;
			// UNIQUE can create the parent and its indexes in this same candidate.
			c.UpdateDescriptor(&next)
		} else {
			stamp, err := revision()
			if err != nil {
				return err
			}
			next.ObjectRevision = stamp
			if err := replace(&next); err != nil {
				return err
			}
		}
	}
	return nil
}
