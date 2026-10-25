package engine

import (
	"stuntgres/internal/engine/table"
)

// table returns the table slot, allocating it on first use.
func tableStage(s *CatalogStage) *table.Stage {
	if s.TableSlot == nil {
		s.TableSlot = new(table.Stage)
	}
	return s.TableSlot
}
