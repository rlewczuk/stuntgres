package table

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Captured row/version vectors and Headers are immutable. Storage identifies the
// heap incarnation only; enumeration never consults its mutable current roots.
type ForeignKeyRows struct {
	Storage                      *storage.Relation
	Committed, Own, Staged       []storage.RowVersion
	Headers                      *storage.HeapHeaderRoot
	Ends                         *storage.DeletionRoot
	PriorDeletes, CurrentDeletes *visibility.PrivateDeletes
	Frontier                     ids.CommitSeq
	Index                        ids.ObjectAddress
	Root                         *storage.IndexRoot
	shadow                       map[foreignKeyHeader]uint8
}

type foreignKeyHeader struct {
	version ids.VersionID
	stamp   uint64
}

// CapturePrivateShadows indexes genuine private identities without copying tuple
// values. Rebuilding replaces the map, preserving every previously captured view.
func (r *ForeignKeyRows) CapturePrivateShadows() {
	r.shadow = nil
	if len(r.Own)+len(r.Staged) == 0 {
		return
	}
	r.shadow = make(map[foreignKeyHeader]uint8, len(r.Own)+len(r.Staged))
	mark := func(rows []storage.RowVersion, tier uint8) {
		for _, row := range rows {
			effective := r.Headers.Apply(row)
			r.shadow[foreignKeyHeader{effective.Version, effective.RepairStamp}] |= tier
			if row.Base != 0 {
				r.shadow[foreignKeyHeader{row.Base, row.BaseHeaderStamp()}] |= tier
			}
			for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
				v := prior.Version
				r.shadow[foreignKeyHeader{v.Version, v.RepairStamp}] |= tier
			}
		}
	}
	mark(r.Own, 1)
	mark(r.Staged, 2)
}

func (r ForeignKeyRows) Each(visit func(storage.RowVersion) (bool, error)) error {
	for _, raw := range r.Committed {
		v := r.Headers.Apply(raw)
		if !r.committedLive(v) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	if stop, err := r.eachPrivate(visit); err != nil || stop {
		return err
	}
	if r.Headers == nil || len(r.Headers.Additional) == 0 {
		return nil
	}
	seen := make(map[ids.VersionID]bool, len(r.Headers.Additional))
	for _, rows := range [][]storage.RowVersion{r.Committed, r.Own, r.Staged} {
		for _, row := range rows {
			seen[row.Version] = true
		}
	}
	for _, raw := range r.Headers.Additional {
		if seen[raw.Version] {
			continue
		}
		seen[raw.Version] = true
		v := r.Headers.Apply(raw)
		if !v.ForcedFrozen || !r.committedLive(v) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	return nil
}

// EachPrivate visits only actual statement/transaction writes. Repaired
// additional heaps do not acquire membership in a supporting index this way.
func (r ForeignKeyRows) EachPrivate(visit func(storage.RowVersion) (bool, error)) error {
	_, err := r.eachPrivate(visit)
	return err
}

func (r ForeignKeyRows) eachPrivate(visit func(storage.RowVersion) (bool, error)) (bool, error) {
	for _, raw := range r.Own {
		v := r.Headers.Apply(raw)
		if r.shadow[foreignKeyHeader{v.Version, v.RepairStamp}]&2 != 0 || !visibility.LiveVersion(v, r.Frontier, nil, r.PriorDeletes) || !visibility.LiveVersion(v, r.Frontier, nil, r.CurrentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return stop, err
		}
	}
	for _, raw := range r.Staged {
		v := r.Headers.Apply(raw)
		if !visibility.LiveVersion(v, r.Frontier, nil, r.CurrentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return stop, err
		}
	}
	return false, nil
}

func (r ForeignKeyRows) Previous(row ids.RowID, version ids.VersionID) (storage.RowVersion, bool) {
	for _, rows := range [][]storage.RowVersion{r.Own, r.Staged} {
		for _, v := range rows {
			if v.Row == row && v.Version == version {
				return r.Headers.Apply(v), true
			}
			for prior := v.PrivateHistory; prior != nil; prior = prior.Next {
				if v := prior.Version; v.Row == row && v.Version == version {
					return r.Headers.Apply(v), true
				}
			}
		}
	}
	for _, raw := range r.Committed {
		v := r.Headers.Apply(raw)
		if v.Row == row && v.Version == version && visibility.LiveVersion(v, r.Frontier, r.Ends, r.PriorDeletes) {
			return v, true
		}
	}
	if r.Headers != nil {
		for _, raw := range r.Headers.Additional {
			v := r.Headers.Apply(raw)
			if v.Row == row && v.Version == version && v.ForcedFrozen && visibility.LiveVersion(v, r.Frontier, r.Ends, r.PriorDeletes) {
				return v, true
			}
		}
	}
	return storage.RowVersion{}, false
}
