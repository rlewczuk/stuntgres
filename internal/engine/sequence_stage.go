package engine

import "stuntgres/internal/engine/sequence"

// SequenceState returns the stage's sequence slot, allocating it on first use.
func (s *CatalogStage) SequenceState() *sequence.Stage {
	if s.Sequence == nil {
		s.Sequence = &sequence.Stage{}
	}
	return s.Sequence
}
