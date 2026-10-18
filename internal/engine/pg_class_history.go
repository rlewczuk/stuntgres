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
			if err := visit(parent); err != nil {
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
	for _, descriptor := range stage.Descriptors {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if err := visitSubclassParents(child, func(address ids.ObjectAddress) error {
			descriptor, staged := stage.Descriptors[address]
			if !staged {
				descriptor = stage.Base.Get(address)
			}
			parent, ok := descriptor.(*catalog.Relation)
			if !ok {
				return fmt.Errorf("stuntgres: invalid subclass parent")
			}
			if parent.HasSubclasses {
				return nil
			}
			next := *parent
			next.HasSubclasses = true
			if staged {
				stage.Descriptors[address] = &next
				return nil
			}
			revision, err := e.catalog.revisionLocked()
			if err != nil {
				return err
			}
			next.ObjectRevision = revision
			next.SubclassPredecessor, next.SubclassPublication = parent, revision
			return stage.Replace(&next)
		}); err != nil {
			return err
		}
	}
	return nil
}

// Replacement's private map may still point at retained immutable descriptors.
// Reconstruct active facts without losing already-retained TEMP history.
func normalizeReplacementSubclassHistory(ctx context.Context, view CatalogRead) error {
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
			if !parent.HasSubclasses {
				next := *parent
				next.HasSubclasses = true
				view.State().SetDescriptor(address, &next)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
