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
	Store         *storage.Relation
	OwnWrites     []storage.RowVersion
	OwnDeletes    *visibility.PrivateDeletes
	StagedWrites  []storage.RowVersion
	StagedDeletes *visibility.PrivateDeletes
	CommitSeq     ids.CommitSeq
	Stable        bool
	Frontier      ids.CommitSeq
}

func (src LatestRowSource) privateVersion(row ids.RowID, version ids.VersionID, hint int) (storage.RowVersion, int, bool) {
	for _, rows := range [2][]storage.RowVersion{src.StagedWrites, src.OwnWrites} {
		if candidate, slot, found := storage.RowByVersion(rows, row, version, hint); found {
			return candidate, slot, true
		}
		for _, candidate := range rows {
			if candidate.Row != row {
				continue
			}
			for prior := candidate.PrivateHistory; prior != nil; prior = prior.Next {
				if prior.Version.Version == version {
					return prior.Version, -1, true
				}
			}
		}
	}
	return storage.RowVersion{}, -1, false
}

func (src LatestRowSource) requested(rows []storage.RowVersion, r RowRequest) (storage.RowVersion, int, bool) {
	if r.Physical == 0 {
		return storage.RowVersion{}, -1, false
	}
	version := r.Physical
	for {
		row, slot, found := storage.RowByVersion(rows, r.Row, version, r.Hint)
		if !found {
			row, slot, found = src.privateVersion(r.Row, version, r.Hint)
		}
		if !found {
			row, slot, found = src.Store.PhysicalVersion(version, r.Hint)
		}
		if !found || row.Row != r.Row {
			return storage.RowVersion{}, -1, false
		}
		row = src.Store.Headers.Apply(row)
		next := src.Store.Headers.Header(version).Successor
		// A forced self-link is reset by repair; only a real later UPDATE
		// can supply a successor. Never guess another same-RowID head.
		if next <= version {
			return row, slot, true
		}
		_, _, ownSuccessor := src.privateVersion(r.Row, next, -1)
		if src.live(row) && !ownSuccessor {
			return row, slot, true
		}
		version = next
	}
}

func (src LatestRowSource) live(row storage.RowVersion) bool {
	return visibility.LiveVersion(row, src.CommitSeq, src.Store.Ends, src.OwnDeletes) && !src.StagedDeletes.Ends(row)
}

// A moved source has no same-relation successor. A concurrent waiter must
// neither resurrect it, silently skip it as DELETE nor chase another leaf.
func (src LatestRowSource) movementConflict(row storage.RowVersion) error {
	if row.ForcedFrozen && row.ForcedEnd == 0 {
		return nil
	}
	if ends := src.Store.Ends; ends != nil {
		if end, ok := ends.Lookup(row.Row, row.Version); ok && end.End <= src.CommitSeq && end.Moved {
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
	row, hint, found := src.requested(current, r)
	if !found {
		return LatestRow{}, errors.New("stuntgres: invalid update predecessor")
	}
	if err := visibility.RepeatableConflict(src.Stable, src.Frontier, row, store.Ends); err != nil {
		return LatestRow{}, err
	}
	if err := src.movementConflict(row); err != nil {
		return LatestRow{}, err
	}
	return LatestRow{Version: row, Hint: hint, Live: src.live(row)}, nil
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
	row, hint, found := src.requested(rows, r)
	if !found {
		return LatestRow{}, errors.New("stuntgres: invalid deletion predecessor")
	}
	if err := visibility.RepeatableConflict(src.Stable, src.Frontier, row, store.Ends); err != nil {
		return LatestRow{}, err
	}
	if err := src.movementConflict(row); err != nil {
		return LatestRow{}, err
	}
	return LatestRow{Version: row, Hint: hint, Live: src.live(row)}, nil
}

// LatestMergeRow prefers the transaction's own target version. Committed
// predecessors outside a retained snapshot conflict before action evaluation.
func LatestMergeRow(src LatestRowSource, r RowRequest) (LatestRow, error) {
	store := src.Store
	if store == nil {
		return LatestRow{}, ids.ErrCatalogReplaced
	}
	row, hint, found := src.requested(store.Versions, r)
	if !found {
		return LatestRow{}, errors.New("stuntgres: invalid MERGE predecessor")
	}
	if err := visibility.RepeatableConflict(src.Stable, src.Frontier, row, store.Ends); err != nil {
		return LatestRow{}, err
	}
	if err := src.movementConflict(row); err != nil {
		return LatestRow{}, err
	}
	return LatestRow{Version: row, Hint: hint, Live: src.live(row)}, nil
}
