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
