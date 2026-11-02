package visibility

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// RepeatableConflict reports the serialization failure of fetching head under
// a stable (repeatable-read or serializable) snapshot that retains frontier:
// head was replaced, or deleted, by a commit after the frontier. Fresh snapshots
// and ordinary unrepaired own writes (Born 0) do not conflict. Repaired heads
// check their committed end first: a retained aborted tuple can also have Born 0.
func RepeatableConflict(stable bool, frontier ids.CommitSeq, head storage.RowVersion, end *storage.DeletionRoot) error {
	if !stable {
		return nil
	}
	if head.ForcedFrozen {
		if head.ForcedEnd > frontier {
			return sqlError("40001", "could not serialize access due to concurrent delete")
		}
		return nil
	}
	if head.Born == 0 {
		return nil
	}
	// The snapshot's predecessor was replaced before its successor was deleted.
	// PostgreSQL reports that first update, rather than the successor's tombstone.
	if head.Born > frontier {
		return sqlError("40001", "could not serialize access due to concurrent update")
	}
	if head.ForcedEnd > frontier {
		return sqlError("40001", "could not serialize access due to concurrent delete")
	}
	if end != nil {
		if tombstone, ok := end.Lookup(head.Row, head.Version); ok && tombstone.End > frontier {
			return sqlError("40001", "could not serialize access due to concurrent delete")
		}
	}
	return nil
}
