package engine

import (
	"stuntgres/internal/engine/visibility"
)

// materializedStage is a catalog stage's materialized-view refresh state:
// the replacement being published and the source snapshot it read. Only
// REFRESH and materialized population write it.
type materializedStage struct {
	replacement *materializedReplacement
	sourceView  *visibility.Data
}

// materialized returns the materialized slot, allocating it on first use.
func (s *CatalogStage) materialized() *materializedStage {
	if s.materializedSlot == nil {
		s.materializedSlot = new(materializedStage)
	}
	return s.materializedSlot
}

// materializedOrZero reads the materialized slot; a nil slot reads as zero.
func (s *CatalogStage) materializedOrZero() materializedStage {
	if s.materializedSlot == nil {
		return materializedStage{}
	}
	return *s.materializedSlot
}
