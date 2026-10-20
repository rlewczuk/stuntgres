package engine

import (
	"stuntgres/internal/sqltype"
)

// Immutable operation provenance: no SQL input callback reaches live session
// state or modifies a definition. Committed captures have no unsafe members.
func EnumSafety(unsafe map[int64]bool) func(sqltype.ID, int64) bool {
	if len(unsafe) == 0 {
		return nil
	}
	return func(_ sqltype.ID, member int64) bool { return !unsafe[member] }
}
