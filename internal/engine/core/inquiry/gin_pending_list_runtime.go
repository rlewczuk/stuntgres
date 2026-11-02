package inquiry

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// GinCleanPendingList admits a live target without manufacturing physical pages.
func GinCleanPendingList(h CatalogHost, oid uint32, _ *sqltype.Descriptor) (sqltype.Value, error) {
	ctx := h.Operation().Context()
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	retained, err := h.InquiryCatalog()
	if err != nil {
		return sqltype.Value{}, err
	}
	if retained.Epoch() != h.CatalogEpochLocked() {
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if err := h.CheckGINPendingListTargetLocked(oid); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, sqlError("0A000", "gin_clean_pending_list physical GIN pending-list cleanup is unsupported in embedded mode")
}
