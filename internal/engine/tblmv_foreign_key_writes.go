package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Captured row/version vectors are immutable. Only shadow identities are indexed;
// values are neither copied nor materialized into an alternate relation.
type foreignKeyRows struct {
	storage                      *storage.Relation
	committed, own, staged       []storage.RowVersion
	ends                         *storage.DeletionRoot
	priorDeletes, currentDeletes *visibility.PrivateDeletes
	shadow                       map[ids.RowID]uint8
	frontier                     ids.CommitSeq
	index                        ids.ObjectAddress
	root                         *storage.IndexRoot
}

func (r foreignKeyRows) each(visit func(storage.RowVersion) (bool, error)) error {
	for _, v := range r.committed {
		if r.shadow[v.Row] != 0 || !visibility.LiveVersion(v, r.frontier, r.ends, r.priorDeletes) || !visibility.LiveVersion(v, r.frontier, r.ends, r.currentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	for _, v := range r.own {
		if r.shadow[v.Row]&2 != 0 || !visibility.LiveVersion(v, r.frontier, nil, r.priorDeletes) || !visibility.LiveVersion(v, r.frontier, nil, r.currentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	for _, v := range r.staged {
		if !visibility.LiveVersion(v, r.frontier, nil, r.currentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	return nil
}

func (r foreignKeyRows) previous(id ids.RowID) (storage.RowVersion, bool) {
	for _, v := range r.own {
		if v.Row == id {
			return v, true
		}
	}
	for _, v := range r.committed {
		if v.Row == id && visibility.LiveVersion(v, r.frontier, r.ends, r.priorDeletes) {
			return v, true
		}
	}
	return storage.RowVersion{}, false
}
