package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
)

// TypedObservationValid reports whether the typed tables a stage observed
// for its source composite type are still exactly the typed closure of
// current.
func TypedObservationValid(typed *typedef.TypedStage, current catalog.Read) bool {
	if typed.SourceType() == (ids.ObjectAddress{}) {
		return true
	}
	observed := TypedClosure(current, typed.SourceType())
	if len(observed) != len(typed.ObservedTables()) {
		return false
	}
	for address, descriptor := range typed.ObservedTables() {
		if observed[address] != descriptor {
			return false
		}
	}
	return true
}
