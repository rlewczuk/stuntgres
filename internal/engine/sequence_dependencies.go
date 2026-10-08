package engine

import "stuntgres/internal/parser"

// AUTO dependencies use stable attribute SubIDs, never compacted column slots.
func (s *StatementScope) stageOwnedSequences(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode) error {
	var closure []ObjectAddress
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
		if closure == nil {
			closure = make([]ObjectAddress, len(targets)+1)
			copy(closure, targets)
		}
		closure[len(targets)] = a
		if err := stageViewDependents(stage, closure, mode); err != nil {
			return err
		}
		ref := ObjectRef{owner: stage.owner.owner, Epoch: stage.base.Epoch(), Address: a}
		// Dependency DDL validates providers outside replacement reservations.
		if err := s.acquireSequence(ref, accessExclusive, false); err != nil {
			return err
		}
		if err := stage.Remove(ref); err != nil {
			return err
		}
	}
	return nil
}
func relationDiagnosticKind(r *relationDescriptor) string {
	if r.relationKind == viewRelation {
		return "view"
	}
	if r.relationKind == sequenceRelation {
		return "sequence"
	}
	return "table"
}
