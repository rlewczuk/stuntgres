package engine

import (
	"stuntgres/internal/engine/ids"
)

// assignTransactionXIDLocked fences ref against the current catalog epoch.
func (e *Engine) assignTransactionXIDLocked(ref ids.TxRef) error {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return e.visibility.AssignTransactionXIDLocked(ref)
}
