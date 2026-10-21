package engine

import (
	"stuntgres/internal/parser"
)

// Only selected EXCLUDE declarations use the public recursive ADD route.
// Private replay origins for other constraint kinds retain their own fences.
func PartitionExclusionAddActions(raw *parser.AlterTable) bool {
	if len(raw.Actions) == 0 {
		return false
	}
	for _, action := range raw.Actions {
		if action.Action != parser.AddExclusion || action.Exclusion == nil {
			return false
		}
	}
	return true
}
