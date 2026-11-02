package exec

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
)

// TupleResource is the sole header-to-manager adapter. The immutable exact
// header, not a RowID head or a visibility flag, owns repaired lock lineage.
func TupleResource(ref ids.ObjectRef, row storage.RowVersion, headers *storage.HeapHeaderRoot) (lock.Resource, error) {
	if row.Version == 0 {
		return lock.Resource{}, ids.ErrCatalogReplaced
	}
	header := headers.Header(row.Version)
	return lock.TupleResource(ref, row.Row, header.TupleLockVersion, header.TupleLockStamp)
}
