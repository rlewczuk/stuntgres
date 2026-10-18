package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// catalogStateFixture builds an unpublished state at epoch that holds
// descriptors and names, through the state's write API.
func catalogStateFixture(epoch ids.CatalogEpoch, descriptors map[ids.ObjectAddress]Descriptor, names map[ids.NameKey]ids.ObjectAddress) *catalogState {
	s := catalog.NewState(epoch)
	for a, d := range descriptors {
		s.SetDescriptor(a, d)
	}
	for k, a := range names {
		s.BindName(k, a)
	}
	return s
}
