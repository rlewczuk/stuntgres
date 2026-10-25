package engine

import (
	"stuntgres/internal/engine/table"
)

// table returns the table slot, allocating it on first use.
func tableStage(s *CatalogStage) *table.Stage {
	if s.tableSlot == nil {
		s.tableSlot = new(table.Stage)
	}
	return s.tableSlot
}
