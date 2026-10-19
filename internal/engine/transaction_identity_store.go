package engine

import (
	"stuntgres/internal/engine/ids"
)

// transactionAgeLocked fences ref against the current catalog epoch.
func (e *Engine) transactionAgeLocked(ref ids.TxRef, xid uint32) (int32, error) {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	return e.visibility.TransactionAgeLocked(ref, xid)
}
