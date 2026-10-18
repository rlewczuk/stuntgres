package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
)

// Visit only owned graph edges. An ordinary index's table is not its subclass
// parent; attached indexes use the separate partitionParent identity.
func visitSubclassParents(child *relationDescriptor, visit func(ids.ObjectAddress) error) error {
	if child.inheritance != nil {
		for _, parent := range child.inheritance.parents {
			if err := visit(parent); err != nil {
				return err
			}
		}
	}
	if child.membership != nil {
		if err := visit(child.membership.parent); err != nil {
			return err
		}
	}
	if child.sqlIndex != nil && child.sqlIndex.partitionParent != (ids.ObjectAddress{}) {
		if err := visit(child.sqlIndex.partitionParent); err != nil {
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
			if parent.hasSubclasses {
				return nil
			}
			next := *parent
			next.hasSubclasses = true
			if staged {
				stage.descriptors[address] = &next
				return nil
			}
			revision, err := e.catalog.revisionLocked()
			if err != nil {
				return err
			}
			next.revision = revision
			next.subclassPredecessor, next.subclassPublication = parent, revision
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
			if !parent.hasSubclasses {
				next := *parent
				next.hasSubclasses = true
				catalog.state.setDescriptor(address, &next)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
