package visibility

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// RepeatableConflict reports the serialization failure of fetching head under
// a stable (repeatable-read or serializable) snapshot that retains frontier:
// head was replaced, or deleted, by a commit after the frontier. It returns nil
// for a fresh snapshot and for an own write (Born 0).
func RepeatableConflict(stable bool, frontier ids.CommitSeq, head storage.RowVersion, end *storage.DeletionRoot) error {
	if !stable || head.Born == 0 {
		return nil
	}
	// The snapshot's predecessor was replaced before its successor was deleted.
	// PostgreSQL reports that first update, rather than the successor's tombstone.
	if head.Born > frontier {
		return sqlError("40001", "could not serialize access due to concurrent update")
	}
	if end != nil {
		if tombstone, ok := end.Rows[head.Row]; ok && tombstone.Version == head.Version && tombstone.End > frontier {
			return sqlError("40001", "could not serialize access due to concurrent delete")
		}
	}
	return nil
}
