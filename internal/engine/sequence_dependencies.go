package engine

import (
	"context"
	"stuntgres/internal/parser"
)

// AUTO dependencies use stable attribute SubIDs, never compacted column slots.
func (s *StatementScope) stageOwnedSequences(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode, changes *catalogChanges) error {
	return stageOwnedExpressionDependencies(s.op.Context(), stage, targets, mode, s, changes, nil)
}

func stageOwnedExpressionDependencies(ctx context.Context, stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *catalogChanges, project func(*relationDescriptor, *relationDescriptor) error) error {
	var closure []ObjectAddress
	extended := false
	// Include removed hierarchy owners, not unrelated removals staged by an
	// earlier pass. Ordinary column projection retains its existing ordering.
	for address, removal := range stage.removals {
		relation, ok := removal.descriptor.(*relationDescriptor)
		if !ok || relation.relationKind != tableRelation || !inheritanceParticipant(stage.base, relation) {
			continue
		}
		present := false
		for _, target := range targets {
			present = present || target == address
		}
		if !present {
			if !extended {
				targets = append([]ObjectAddress(nil), targets...)
				extended = true
			}
			targets = append(targets, address)
		}
	}
	return stageOwnedSequenceDependencies(stage, targets, mode, func(ref ObjectRef) error {
		if closure == nil {
			closure = make([]ObjectAddress, len(targets)+1)
			copy(closure, targets)
		}
		closure[len(targets)] = ref.Address
		if err := stageExpressionDependencies(ctx, stage, closure, mode, scope, changes, project); err != nil {
			return err
		}
		if scope != nil {
			return scope.acquireSequence(ref, accessExclusive, false)
		}
		return nil
	})
}

// Every owner supplies the same complete expression traversal; real SQL
// additionally acquires its existing grants. No view-only automatic fallback.
func stageOwnedSequenceDependencies(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode, prepare func(ObjectRef) error) error {
	for a, d := range stage.base.state.descriptors {
		if _, removed := stage.removals[a]; removed {
			continue
		}
		if replacement, changed := stage.descriptors[a]; changed {
			d = replacement
		}
		r, ok := d.(*relationDescriptor)
		if !ok || r.relationKind != sequenceRelation || r.sequence.owned == (ObjectAddress{}) {
			continue
		}
		owned := r.sequence.owned
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
		ref := ObjectRef{owner: stage.owner.owner, Epoch: stage.base.Epoch(), Address: a}
		if err := prepare(ref); err != nil {
			return err
		}
		if err := stage.Remove(ref); err != nil {
			return err
		}
	}
	return nil
}
func relationDiagnosticKind(r *relationDescriptor) string {
	if r.relationKind == compositeRelation {
		return "composite type"
	}
	if r.relationKind == materializedViewRelation {
		return "materialized view"
	}
	if r.relationKind == indexRelation {
		return "index"
	}
	if r.relationKind == viewRelation {
		return "view"
	}
	if r.relationKind == sequenceRelation {
		return "sequence"
	}
	return "table"
}
