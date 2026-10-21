package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Dependency mutation sees the private descriptor authority, not stale base
// edges. Scan the overlay directly rather than allocating a second catalog.
func inheritanceStageAlterGuard(stage *CatalogStage, relation *catalog.Relation) error {
	descriptors := func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range stage.Base.Descriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
		for address, descriptor := range stage.Descriptors() {
			if _, exists := stage.Base.Find(address); !exists {
				if !yield(address, descriptor) {
					return
				}
			}
		}
	}
	return InheritanceDependencyAlterGuard(descriptors, func(address ids.ObjectAddress) catalog.Descriptor { return stage.Get(address) }, func(address ids.ObjectAddress) bool { _, removed := stage.Removal(address); return removed }, relation)
}
