package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Called under Engine.mu at the existing preallocation and final TEMP-retention
// admission points. A replacement cannot retarget an attachment by name or OID.
func (e *Engine) checkTemporaryTypedReplacementLocked() error {
	for _, descriptor := range e.catalog.state.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.OfType == (ids.ObjectAddress{}) {
			continue
		}
		namespace := e.catalog.state.MustNamespace(table.Namespace())
		if namespace.Temporary == (ids.SessionToken{}) {
			continue
		}
		source, ok := e.catalog.state.Type(table.OfType)
		if !ok {
			return fmt.Errorf("stuntgres: missing typed source")
		}
		if e.catalog.state.MustNamespace(source.ObjectNamespace).Temporary == (ids.SessionToken{}) {
			return fmt.Errorf("%s", TemporaryTypedReplacementError)
		}
	}
	return nil
}
