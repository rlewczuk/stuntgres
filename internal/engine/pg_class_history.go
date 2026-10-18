package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
)

// Visit only owned graph edges. An ordinary index's table is not its subclass
// parent; attached indexes use the separate partitionParent identity.
func visitSubclassParents(child *relationDescriptor, visit func(ids.ObjectAddress) error) error {
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
	for _, descriptor := range stage.descriptors {
		child, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if err := visitSubclassParents(child, func(address ids.ObjectAddress) error {
			descriptor, staged := stage.descriptors[address]
			if !staged {
				descriptor = stage.base.Get(address)
			}
			parent, ok := descriptor.(*relationDescriptor)
			if !ok {
				return fmt.Errorf("stuntgres: invalid subclass parent")
			}
			if parent.HasSubclasses {
				return nil
			}
			next := *parent
			next.HasSubclasses = true
			if staged {
				stage.descriptors[address] = &next
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
func normalizeReplacementSubclassHistory(ctx context.Context, catalog CatalogRead) error {
	for _, descriptor := range catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		child, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if err := visitSubclassParents(child, func(address ids.ObjectAddress) error {
			parent, ok := catalog.Relation(address)
			if !ok {
				return fmt.Errorf("stuntgres: invalid subclass parent")
			}
			if !parent.HasSubclasses {
				next := *parent
				next.HasSubclasses = true
				catalog.state.setDescriptor(address, &next)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
