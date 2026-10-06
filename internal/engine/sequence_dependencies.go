package engine

// AUTO dependencies use stable attribute SubIDs, never compacted column slots.
func (s *StatementScope) stageOwnedSequences(stage *CatalogStage, targets []ObjectAddress) error {
	for a, d := range stage.base.state.descriptors {
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
		if err := s.acquireSequence(ref, accessExclusive); err != nil {
			return err
		}
		if err := stage.Remove(ref); err != nil {
			return err
		}
	}
	return nil
}
func relationDiagnosticKind(r *relationDescriptor) string {
	if r.relationKind == sequenceRelation {
		return "sequence"
	}
	return "table"
}
