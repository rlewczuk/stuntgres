package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/table"
)

// tableOrZero reads the table slot; a nil slot reads as zero.
func (s *CatalogStage) tableOrZero() table.Stage {
	if s.TableSlot == nil {
		return table.Stage{}
	}
	return *s.TableSlot
}

// validateLikeSources checks the stage's LIKE sources against current.
func (s *CatalogStage) validateLikeSources(current catalog.Read) error {
	return s.TableSlot.ValidateLikeSources(s.Base, s, current)
}
