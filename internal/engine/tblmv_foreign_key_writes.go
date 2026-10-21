package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Captured row/version vectors are immutable. Only shadow identities are indexed;
// values are neither copied nor materialized into an alternate relation.
type foreignKeyRows struct {
	Storage                      *storage.Relation
	Committed, Own, Staged       []storage.RowVersion
	Ends                         *storage.DeletionRoot
	PriorDeletes, CurrentDeletes *visibility.PrivateDeletes
	Shadow                       map[ids.RowID]uint8
	Frontier                     ids.CommitSeq
	Index                        ids.ObjectAddress
	Root                         *storage.IndexRoot
}

func (r foreignKeyRows) Each(visit func(storage.RowVersion) (bool, error)) error {
	for _, v := range r.Committed {
		if r.Shadow[v.Row] != 0 || !visibility.LiveVersion(v, r.Frontier, r.Ends, r.PriorDeletes) || !visibility.LiveVersion(v, r.Frontier, r.Ends, r.CurrentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	for _, v := range r.Own {
		if r.Shadow[v.Row]&2 != 0 || !visibility.LiveVersion(v, r.Frontier, nil, r.PriorDeletes) || !visibility.LiveVersion(v, r.Frontier, nil, r.CurrentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	for _, v := range r.Staged {
		if !visibility.LiveVersion(v, r.Frontier, nil, r.CurrentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	return nil
}

func (r foreignKeyRows) Previous(id ids.RowID) (storage.RowVersion, bool) {
	for _, v := range r.Own {
		if v.Row == id {
			return v, true
		}
	}
	for _, v := range r.Committed {
		if v.Row == id && visibility.LiveVersion(v, r.Frontier, r.Ends, r.PriorDeletes) {
			return v, true
		}
	}
	return storage.RowVersion{}, false
}
