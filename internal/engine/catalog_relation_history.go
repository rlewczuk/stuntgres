package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Visit only owned graph edges. An ordinary index's table is not its subclass
// parent; attached indexes use the separate partitionParent identity.
func visitSubclassParents(child *catalog.Relation, visit func(ids.ObjectAddress) error) error {
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

func (e *Engine) markStageSubclassHistoryLocked(stage *CatalogStage) error {
	for _, descriptor := range stage.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if err := visitSubclassParents(child, func(address ids.ObjectAddress) error {
			descriptor, staged := stage.Find(address)
			if !staged {
				descriptor = stage.Base.Get(address)
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
				stage.UpdateDescriptor(&next)
				return nil
			}
			revision, err := e.catalog.revisionLocked()
			if err != nil {
				return err
			}
			next.ObjectRevision = revision
			next.History.SubclassPredecessor, next.History.SubclassPublication = parent, revision
			return stage.Replace(&next)
		}); err != nil {
			return err
		}
	}
	return nil
}

// Replacement's private map may still point at retained immutable descriptors.
// Reconstruct active facts without losing already-retained TEMP history.
func normalizeReplacementSubclassHistory(ctx context.Context, view catalog.Read) error {
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if err := visitSubclassParents(child, func(address ids.ObjectAddress) error {
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

// Index publication and parent history are one immutable catalog candidate.
// DROP deliberately never clears this flag (PostgreSQL18 pg_class lazy flags).
func (e *Engine) markStageIndexHistoryLocked(stage *CatalogStage) error {
	for _, descriptor := range stage.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		address := index.SQLIndex.Parent
		descriptor, staged := stage.Find(address)
		if !staged {
			descriptor = stage.Base.Get(address)
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
			stage.UpdateDescriptor(&next)
		} else {
			revision, err := e.catalog.revisionLocked()
			if err != nil {
				return err
			}
			next.ObjectRevision = revision
			if err := stage.Replace(&next); err != nil {
				return err
			}
		}
	}
	return nil
}

// Replacement admission owns private descriptor maps, but its descriptors still
// belong to retained source snapshots. Normalize represented indexes into that
// candidate without mutating source roots or clearing history-only true flags.
// Structural index validation remains independent of this publication metadata.
func normalizeReplacementIndexHistory(ctx context.Context, view catalog.Read) error {
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent, ok := view.Relation(index.SQLIndex.Parent)
		if !ok || (parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.MaterializedViewRelation && parent.RelationKind != catalog.PartitionedRelation) {
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
