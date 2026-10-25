package engine

import "stuntgres/internal/engine/core"

import "stuntgres/internal/engine/typedef"

// typedStage returns the stage's typed-table slot, allocating it on first use.
func typedStage(s *core.CatalogStage) *typedef.TypedStage {
	if s.Typed == nil {
		s.Typed = new(typedef.TypedStage)
	}
	return s.Typed
}
