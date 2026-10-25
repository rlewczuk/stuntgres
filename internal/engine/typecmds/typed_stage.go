package typecmds

import "stuntgres/internal/engine/core"

import "stuntgres/internal/engine/typedef"

// TypedStage returns the stage's typed-table slot, allocating it on first use.
func TypedStage(s *core.CatalogStage) *typedef.TypedStage {
	if s.Typed == nil {
		s.Typed = new(typedef.TypedStage)
	}
	return s.Typed
}
