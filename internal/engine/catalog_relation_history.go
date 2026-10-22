package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (e *Engine) markStageSubclassHistoryLocked(stage *CatalogStage) error {
	for _, descriptor := range stage.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if err := catalog.VisitSubclassParents(child, func(address ids.ObjectAddress) error {
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
