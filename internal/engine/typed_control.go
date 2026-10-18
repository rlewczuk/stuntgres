package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

const temporaryTypedReplacementError = "stuntgres: catalog replacement with live temporary typed tables of persistent types is unsupported"

// Called under Engine.mu at the existing preallocation and final TEMP-retention
// admission points. A replacement cannot retarget an attachment by name or OID.
func (e *Engine) checkTemporaryTypedReplacementLocked() error {
	for _, descriptor := range e.catalog.state.Descriptors() {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || table.ofType == (ids.ObjectAddress{}) {
			continue
		}
		namespace := e.catalog.state.MustNamespace(table.namespace)
		if namespace.temporary == (ids.SessionToken{}) {
			continue
		}
		source, ok := e.catalog.state.Type(table.ofType)
		if !ok {
			return fmt.Errorf("stuntgres: missing typed source")
		}
		if e.catalog.state.MustNamespace(source.namespace).temporary == (ids.SessionToken{}) {
			return fmt.Errorf("%s", temporaryTypedReplacementError)
		}
	}
	return nil
}
