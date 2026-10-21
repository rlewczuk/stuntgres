package engine

import "stuntgres/internal/engine/view"

// materialized returns the materialized slot, allocating it on first use.
func (s *CatalogStage) materialized() *view.MaterializedStage {
	if s.materializedSlot == nil {
		s.materializedSlot = new(view.MaterializedStage)
	}
	return s.materializedSlot
}

// materializedOrZero reads the materialized slot; a nil slot reads as zero.
func (s *CatalogStage) materializedOrZero() view.MaterializedStage {
	if s.materializedSlot == nil {
		return view.MaterializedStage{}
	}
	return *s.materializedSlot
}
