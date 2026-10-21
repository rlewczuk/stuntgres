package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func typedObservationValid(stage *CatalogStage, current catalog.Read) bool {
	if stage.typed.SourceType() == (ids.ObjectAddress{}) {
		return true
	}
	observed := TypedClosure(current, stage.typed.SourceType())
	if len(observed) != len(stage.typed.ObservedTables()) {
		return false
	}
	for address, descriptor := range stage.typed.ObservedTables() {
		if observed[address] != descriptor {
			return false
		}
	}
	return true
}
