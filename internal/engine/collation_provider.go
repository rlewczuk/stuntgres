package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Captured provider manifests may outlive a descriptor revision, but never
// authorize a missing identity, an attribute address, or an unavailable provider.
func ValidateCollationProvider(view catalog.Read, ref ids.ObjectRef) error {
	if ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid stored collation provider")
	}
	d, ok := view.Collation(ref.Address)
	if !ok {
		return fmt.Errorf("stuntgres: invalid stored collation provider")
	}
	if capability, err := d.Usage(); err != nil || capability.ID != uint32(ref.Address.ObjectID) {
		return fmt.Errorf("stuntgres: invalid stored collation provider")
	}
	return nil
}
