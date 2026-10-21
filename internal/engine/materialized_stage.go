package engine

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
