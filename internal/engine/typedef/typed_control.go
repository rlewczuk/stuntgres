package typedef

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

const TemporaryTypedReplacementError = "stuntgres: catalog replacement with live temporary typed tables of persistent types is unsupported"

// CheckTemporaryTypedReplacementLocked rejects a catalog replacement while a
// TEMP typed table of the published state has a persistent OF type: a
// replacement cannot retarget the attachment by name or OID. The caller holds
// Engine.mu, at the preallocation and final TEMP-retention admission points.
func CheckTemporaryTypedReplacementLocked(state *catalog.State) error {
	for _, descriptor := range state.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.OfType == (ids.ObjectAddress{}) {
			continue
		}
		namespace := state.MustNamespace(table.Namespace())
		if namespace.Temporary == (ids.SessionToken{}) {
			continue
		}
		source, ok := state.Type(table.OfType)
		if !ok {
			return fmt.Errorf("stuntgres: missing typed source")
		}
		if state.MustNamespace(source.ObjectNamespace).Temporary == (ids.SessionToken{}) {
			return fmt.Errorf("%s", TemporaryTypedReplacementError)
		}
	}
	return nil
}
