package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

const temporaryTypedReplacementError = "stuntgres: catalog replacement with live temporary typed tables of persistent types is unsupported"

// Called under Engine.mu at the existing preallocation and final TEMP-retention
// admission points. A replacement cannot retarget an attachment by name or OID.
func (e *Engine) checkTemporaryTypedReplacementLocked() error {
	for _, descriptor := range e.state.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || table.ofType == (ids.ObjectAddress{}) {
			continue
		}
		namespace := e.state.descriptors[table.namespace].(*namespaceDescriptor)
		if namespace.temporary == (SessionToken{}) {
			continue
		}
		source, ok := e.state.descriptors[table.ofType].(*typeDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing typed source")
		}
		if e.state.descriptors[source.namespace].(*namespaceDescriptor).temporary == (SessionToken{}) {
			return fmt.Errorf("%s", temporaryTypedReplacementError)
		}
	}
	return nil
}
