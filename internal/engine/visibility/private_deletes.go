package visibility

import (
	"iter"
	"maps"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Lookup prefers an exact selected version over a replacement's predecessor.
// A Base match ends only that genuine lineage, never another same-RowID sibling.
func (r *PrivateDeletes) Lookup(row ids.RowID, version ids.VersionID) (DeleteCandidate, bool) {
	if r == nil {
		return DeleteCandidate{}, false
	}
	primary, present := r.Rows[row]
	if present && primary.Version == version {
		return primary, true
	}
	if candidate, ok := r.Versions[version]; ok && candidate.Row == row {
		return candidate, true
	}
	if present && primary.Base != 0 && primary.Base == version {
		return primary, true
	}
	for _, candidate := range r.Versions {
		if candidate.Row == row && candidate.Base != 0 && candidate.Base == version {
			return candidate, true
		}
	}
	return DeleteCandidate{}, false
}

// Ends applies only this overlay's exact physical deletion and repair-admission
// authority. Birth/frontier and committed ends belong to the heap's LiveVersion.
func (r *PrivateDeletes) Ends(v storage.RowVersion) bool {
	deleted, ok := r.Lookup(v.Row, v.Version)
	if !ok {
		return false
	}
	stamp := deleted.RepairStamp
	if deleted.Version != v.Version {
		stamp = deleted.BaseRepairStamp
	}
	if !v.ForcedFrozen || stamp == v.RepairStamp {
		return true
	}
	if deleted.Version != v.Version {
		for row, candidate := range r.All() {
			if row == v.Row && candidate.Base == v.Version && candidate.BaseRepairStamp == v.RepairStamp {
				return true
			}
		}
	}
	return false
}

func (r *PrivateDeletes) All() iter.Seq2[ids.RowID, DeleteCandidate] {
	return func(yield func(ids.RowID, DeleteCandidate) bool) {
		if r == nil {
			return
		}
		for row, candidate := range r.Rows {
			if !yield(row, candidate) {
				return
			}
		}
		for _, candidate := range r.Versions {
			if !yield(candidate.Row, candidate) {
				return
			}
		}
	}
}

func (r *PrivateDeletes) Len() int {
	if r == nil {
		return 0
	}
	return len(r.Rows) + len(r.Versions)
}

func (r *PrivateDeletes) Clone() *PrivateDeletes {
	if r == nil {
		return nil
	}
	return &PrivateDeletes{Rows: maps.Clone(r.Rows), Versions: maps.Clone(r.Versions)}
}

// Set mutates only an unpublished overlay and never overwrites a different
// physical sibling's deletion solely because it has the same logical RowID.
func (r *PrivateDeletes) Set(candidate DeleteCandidate) {
	if _, exists := r.Versions[candidate.Version]; exists {
		r.Versions[candidate.Version] = candidate
		return
	}
	if prior, ok := r.Rows[candidate.Row]; !ok || prior.Version == candidate.Version {
		if r.Rows == nil {
			r.Rows = make(map[ids.RowID]DeleteCandidate)
		}
		r.Rows[candidate.Row] = candidate
		return
	}
	if r.Versions == nil {
		r.Versions = make(map[ids.VersionID]DeleteCandidate)
	}
	r.Versions[candidate.Version] = candidate
}

// Delete removes an exact selected Version, not a candidate found by Base.
func (r *PrivateDeletes) Delete(row ids.RowID, version ids.VersionID) {
	if r == nil {
		return
	}
	if candidate, ok := r.Rows[row]; ok && candidate.Version == version {
		delete(r.Rows, row)
		for id, sibling := range r.Versions {
			if sibling.Row == row {
				r.Rows[row] = sibling
				delete(r.Versions, id)
				break
			}
		}
		return
	}
	if candidate, ok := r.Versions[version]; ok && candidate.Row == row {
		delete(r.Versions, version)
	}
}
