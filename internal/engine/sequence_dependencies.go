package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// AUTO dependencies use stable attribute SubIDs, never compacted column slots.
func (s *StatementScope) stageOwnedSequences(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, changes *catalogChanges) error {
	return stageOwnedExpressionDependencies(s.op.Context(), stage, targets, mode, s, changes, nil)
}

func stageOwnedExpressionDependencies(ctx context.Context, stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *catalogChanges, project func(*relationDescriptor, *relationDescriptor) error) error {
	return stageDropProvider(ctx, stage, scope, changes, project).ownedExpressionDependencies(targets, mode)
}

func (provider *dropProvider) ownedExpressionDependencies(targets []ids.ObjectAddress, mode parser.DropMode) error {
	var closure []ids.ObjectAddress
	extended := false
	// Include removed hierarchy owners, not unrelated removals staged by an
	// earlier pass. Ordinary column projection retains its existing ordering.
	for address, descriptor := range provider.removalDescriptors() {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok || relation.RelationKind != tableRelation || !inheritanceParticipant(provider.base, relation) {
			continue
		}
		present := false
		for _, target := range targets {
			present = present || target == address
		}
		if !present {
			if !extended {
				targets = append([]ids.ObjectAddress(nil), targets...)
				extended = true
			}
			targets = append(targets, address)
		}
	}
	return provider.ownedSequences(targets, func(ref ids.ObjectRef) error {
		if closure == nil {
			closure = make([]ids.ObjectAddress, len(targets)+1)
			copy(closure, targets)
		}
		closure[len(targets)] = ref.Address
		if err := provider.expressionDependencies(closure, mode, ids.ObjectAddress{}); err != nil {
			return err
		}
		if provider.scope != nil {
			return provider.acquire(ref, false)
		}
		return nil
	})
}

// Every owner supplies the same complete expression traversal; real SQL
// additionally acquires its existing grants. No view-only automatic fallback.
func stageOwnedSequenceDependencies(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, prepare func(ids.ObjectRef) error) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).ownedSequences(targets, prepare)
}

func (provider *dropProvider) ownedSequences(targets []ids.ObjectAddress, prepare func(ids.ObjectRef) error) error {
	for a, d := range provider.inputDescriptors() {
		if provider.removed(a) {
			continue
		}
		if replacement := provider.replacement(a); replacement != nil {
			d = replacement
		}
		r, ok := d.(*relationDescriptor)
		if !ok || r.RelationKind != sequenceRelation || r.Sequence.Owned == (ids.ObjectAddress{}) {
			continue
		}
		owned := r.Sequence.Owned
		remove := false
		for _, target := range targets {
			if owned.ClassID == target.ClassID && owned.ObjectID == target.ObjectID && (target.SubID == 0 || owned.SubID == target.SubID) {
				remove = true
				break
			}
		}
		if !remove {
			continue
		}
		ref := provider.owner.catalog.Domain.Ref(provider.base.Epoch(), a)
		if err := prepare(ref); err != nil {
			return err
		}
		if err := provider.remove(ref); err != nil {
			return err
		}
	}
	return nil
}
func relationDiagnosticKind(r *relationDescriptor) string {
	if r.RelationKind == foreignRelation {
		return "foreign table"
	}
	if r.RelationKind == compositeRelation {
		return "composite type"
	}
	if r.RelationKind == materializedViewRelation {
		return "materialized view"
	}
	if r.RelationKind == indexRelation {
		return "index"
	}
	if r.RelationKind == viewRelation {
		return "view"
	}
	if r.RelationKind == sequenceRelation {
		return "sequence"
	}
	return "table"
}
