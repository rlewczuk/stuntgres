package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// validateRegisteredType validates a type descriptor through the type
// provider registered in the catalog owner. Installed codec types use it for
// their associated array, so extension validation never names engine type
// code; the registry is immutable after construction.
func validateRegisteredType(v catalog.Read, t *catalog.Type) error {
	var provider catalog.Provider
	if owner := v.Owner(); owner != nil {
		provider = owner.Provider(ids.TypeClassID)
	}
	if provider == nil {
		return fmt.Errorf("stuntgres: missing type provider")
	}
	return provider.Validate(v, t)
}
