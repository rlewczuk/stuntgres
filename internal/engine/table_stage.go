package engine

// table returns the table slot, allocating it on first use.
func (s *CatalogStage) table() *tableStage {
	if s.tableSlot == nil {
		s.tableSlot = new(tableStage)
	}
	return s.tableSlot
}

// tableOrZero reads the table slot; a nil slot reads as zero.
func (s *CatalogStage) tableOrZero() tableStage {
	if s.tableSlot == nil {
		return tableStage{}
	}
	return *s.tableSlot
}
