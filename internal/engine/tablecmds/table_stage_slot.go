package tablecmds

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/table"
)

// table returns the table slot, allocating it on first use.
func tableStage(s *core.CatalogStage) *table.Stage {
	if s.TableSlot == nil {
		s.TableSlot = new(table.Stage)
	}
	return s.TableSlot
}
