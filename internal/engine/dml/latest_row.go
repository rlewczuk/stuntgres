package dml

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// LatestRowSource is the locked state a latest-row read decides over: the
// relation's storage as the transaction sees it (nil once replaced), the
// transaction's own writes and deletions of that relation, the commit
// sequence, and whether the snapshot is stable with its frontier. The caller
// captures it under Engine.mu after its catalog epoch checks; the readers take
// no lock and never wait.
type LatestRowSource struct {
	Store      *storage.Relation
	OwnWrites  []storage.RowVersion
	OwnDeletes *visibility.PrivateDeletes
	CommitSeq  ids.CommitSeq
	Stable     bool
	Frontier   ids.CommitSeq
}

func (src LatestRowSource) live(row storage.RowVersion) bool {
	return visibility.LiveVersion(row, src.CommitSeq, src.Store.Ends, src.OwnDeletes)
}

// A moved source has no same-relation successor. A concurrent waiter must
// neither resurrect it, silently skip it as DELETE nor chase another leaf.
func (src LatestRowSource) movementConflict(row storage.RowVersion) error {
	if ends := src.Store.Ends; ends != nil {
		if end, ok := ends.Rows[row.Row]; ok && end.Version == row.Version && end.End <= src.CommitSeq && end.Moved {
			return sqlError("40001", "tuple to be locked was already moved to another partition due to concurrent update")
		}
	}
	return nil
}

// LatestUpdateRow reads an UPDATE predecessor; it conflicts under a retained
// snapshot.
func LatestUpdateRow(src LatestRowSource, r RowRequest) (LatestRow, error) {
	store := src.Store
	if store == nil {
		return LatestRow{}, ids.ErrCatalogReplaced
	}
	current := store.Versions
	if r.Own {
		current = src.OwnWrites
	}
	row, _, found := storage.RowByID(current, r.Row, r.Hint)
	if !found {
		return LatestRow{}, errors.New("stuntgres: invalid update predecessor")
	}
	if err := visibility.RepeatableConflict(src.Stable, src.Frontier, row, store.Ends); err != nil {
		return LatestRow{}, err
	}
	if err := src.movementConflict(row); err != nil {
		return LatestRow{}, err
	}
	return LatestRow{Version: row, Live: src.live(row)}, nil
}

// LatestDeleteRow reads a DELETE predecessor. Row arbitration freezes
// identity, not a stale ordinal or pre-wait predicate.
func LatestDeleteRow(src LatestRowSource, r RowRequest) (LatestRow, error) {
	store := src.Store
	if store == nil || store != r.Storage {
		return LatestRow{}, ids.ErrCatalogReplaced
	}
	rows := store.Versions
	if r.Own {
		rows = src.OwnWrites
	}
	row, _, found := storage.RowByID(rows, r.Row, r.Hint)
	if !found {
		return LatestRow{}, errors.New("stuntgres: invalid deletion predecessor")
	}
	if err := visibility.RepeatableConflict(src.Stable, src.Frontier, row, store.Ends); err != nil {
		return LatestRow{}, err
	}
	if err := src.movementConflict(row); err != nil {
		return LatestRow{}, err
	}
	return LatestRow{Version: row, Live: src.live(row)}, nil
}

// LatestMergeRow reads a MERGE target row. MERGE follows a committed update
// even with a retained RR frontier. Deletion still conflicts; query
// visibility, not this fetch, owns old+own occurrences.
func LatestMergeRow(src LatestRowSource, r RowRequest) (LatestRow, error) {
	store := src.Store
	if store == nil {
		return LatestRow{}, ids.ErrCatalogReplaced
	}
	current, hint := store.Versions, r.Hint
	if own, _, found := storage.RowByID(src.OwnWrites, r.Row, -1); found {
		return LatestRow{Version: own, Live: src.live(own)}, nil
	}
	row, _, found := storage.RowByID(current, r.Row, hint)
	if !found {
		return LatestRow{}, errors.New("stuntgres: invalid MERGE predecessor")
	}
	live := src.live(row)
	if !live && src.Stable && store.Ends != nil {
		if tombstone, ok := store.Ends.Rows[row.Row]; ok && tombstone.Version == row.Version && tombstone.End > src.Frontier {
			return LatestRow{}, sqlError("40001", "could not serialize access due to concurrent delete")
		}
	}
	return LatestRow{Version: row, Live: live}, nil
}
