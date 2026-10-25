package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/table"
)

// foreignKeysOrZero reads the foreign-key slot; a nil slot reads as zero.
func foreignKeysOrZero(s *core.StatementScope) table.ForeignKeyCreation {
	if s.ForeignKeysSlot == nil {
		return table.ForeignKeyCreation{}
	}
	return *s.ForeignKeysSlot
}
