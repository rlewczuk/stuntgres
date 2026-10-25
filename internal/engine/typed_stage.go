package engine

import "stuntgres/internal/engine/typedef"

// typedState returns the stage's typed-table slot, allocating it on first use.
func (s *CatalogStage) typedState() *typedef.TypedStage {
	if s.Typed == nil {
		s.Typed = new(typedef.TypedStage)
	}
	return s.Typed
}
