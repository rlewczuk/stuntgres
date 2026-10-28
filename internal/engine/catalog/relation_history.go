package catalog

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
)

// Visit only owned graph edges. An ordinary index's table is not its subclass
// parent; attached indexes use the separate partitionParent identity.
func VisitSubclassParents(child *Relation, visit func(ids.ObjectAddress) error) error {
	if child.Inheritance != nil {
		for _, parent := range child.Inheritance.Parents {
			if err := visit(parent.Relation); err != nil {
				return err
			}
		}
	}
	if child.Membership != nil {
		if err := visit(child.Membership.Parent); err != nil {
			return err
		}
	}
	if child.SQLIndex != nil && child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		if err := visit(child.SQLIndex.PartitionParent); err != nil {
			return err
		}
	}
	return nil
}

// Replacement's private map may still point at retained immutable descriptors.
// Reconstruct active facts without losing already-retained TEMP history.
func NormalizeReplacementSubclassHistory(ctx context.Context, view Read) error {
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		child, ok := descriptor.(*Relation)
		if !ok {
			continue
		}
		if child.History.DetachPredecessor != nil || child.History.DetachPublication != 0 || child.History.DetachRoot != (ids.ObjectAddress{}) {
			next := *child
			next.History.DetachPredecessor = nil
			next.History.DetachPublication = 0
			next.History.DetachRoot = ids.ObjectAddress{}
			child = &next
			view.State().SetDescriptor(child.Address(), child)
		}
		if err := VisitSubclassParents(child, func(address ids.ObjectAddress) error {
			parent, ok := view.Relation(address)
			if !ok {
				return fmt.Errorf("stuntgres: invalid subclass parent")
			}
			if !parent.History.HasSubclasses {
				next := *parent
				next.History.HasSubclasses = true
				view.State().SetDescriptor(address, &next)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// Replacement admission owns private descriptor maps, but its descriptors still
// belong to retained source snapshots. Normalize represented indexes into that
// candidate without mutating source roots or clearing history-only true flags.
// Structural index validation remains independent of this publication metadata.
func NormalizeReplacementIndexHistory(ctx context.Context, view Read) error {
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		index, ok := descriptor.(*Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent, ok := view.Relation(index.SQLIndex.Parent)
		if !ok || (parent.RelationKind != TableRelation && parent.RelationKind != MaterializedViewRelation && parent.RelationKind != PartitionedRelation) {
			return fmt.Errorf("stuntgres: invalid index parent")
		}
		if !parent.History.HasIndexes {
			next := *parent
			next.History.HasIndexes = true
			view.State().SetDescriptor(parent.Address(), &next)
		}
	}
	return nil
}

// MarkSubclassHistoryLocked sets the sticky HasSubclasses flag on every parent
// of a staged child. A staged parent is completed in place; a base parent is
// staged through replace at a fresh revision from revision. The root passes
// its allocator and CatalogStage.Replace, which also synchronizes the row
// type, and calls it under Engine.mu.
func (c *Candidate) MarkSubclassHistoryLocked(revision func() (ids.DefinitionRevision, error), replace func(Descriptor) error) error {
	for _, descriptor := range c.Descriptors() {
		child, ok := descriptor.(*Relation)
		if !ok {
			continue
		}
		if err := VisitSubclassParents(child, func(address ids.ObjectAddress) error {
			descriptor, staged := c.Find(address)
			if !staged {
				descriptor = c.Base.Get(address)
			}
			parent, ok := descriptor.(*Relation)
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

// MarkIndexHistoryLocked sets the sticky HasIndexes flag on the parent of every
// staged index; revision and replace are as for MarkSubclassHistoryLocked.
// Index publication and parent history are one immutable catalog candidate.
// DROP deliberately never clears this flag (PostgreSQL18 pg_class lazy flags).
func (c *Candidate) MarkIndexHistoryLocked(revision func() (ids.DefinitionRevision, error), replace func(Descriptor) error) error {
	for _, descriptor := range c.Descriptors() {
		index, ok := descriptor.(*Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		address := index.SQLIndex.Parent
		descriptor, staged := c.Find(address)
		if !staged {
			descriptor = c.Base.Get(address)
		}
		parent, ok := descriptor.(*Relation)
		if !ok || (parent.RelationKind != TableRelation && parent.RelationKind != MaterializedViewRelation && parent.RelationKind != PartitionedRelation) {
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
