package engine

import "stuntgres/internal/engine/sequence"

// sequenceState returns the stage's sequence slot, allocating it on first use.
func (s *CatalogStage) sequenceState() *sequence.Stage {
	if s.sequence == nil {
		s.sequence = &sequence.Stage{}
	}
	return s.sequence
}
