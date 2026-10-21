package engine

import (
	"stuntgres/internal/engine/catalog"
)

// validateLikeSources checks the stage's LIKE sources against current.
func (s *CatalogStage) validateLikeSources(current catalog.Read) error {
	return s.tableSlot.ValidateLikeSources(s.Base, s, current)
}
