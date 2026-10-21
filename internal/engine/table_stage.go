package engine

import "stuntgres/internal/engine/table"

// table returns the table slot, allocating it on first use.
func (s *CatalogStage) table() *table.Stage {
	if s.tableSlot == nil {
		s.tableSlot = new(table.Stage)
	}
	return s.tableSlot
}

// tableOrZero reads the table slot; a nil slot reads as zero.
func (s *CatalogStage) tableOrZero() table.Stage {
	if s.tableSlot == nil {
		return table.Stage{}
	}
	return *s.tableSlot
}
