package storage

import (
	"iter"
	"maps"

	"stuntgres/internal/engine/ids"
)

func (r *DeletionRoot) Lookup(row ids.RowID, version ids.VersionID) (RowEnd, bool) {
	if r != nil {
		if end, ok := r.Rows[row]; ok && end.Version == version {
			end.Row = row
			return end, true
		}
		if end, ok := r.Versions[version]; ok && end.Row == row {
			return end, true
		}
	}
	return RowEnd{}, false
}

func (r *DeletionRoot) All() iter.Seq2[ids.RowID, RowEnd] {
	return func(yield func(ids.RowID, RowEnd) bool) {
		if r == nil {
			return
		}
		for row, end := range r.Rows {
			end.Row = row
			if !yield(row, end) {
				return
			}
		}
		for _, end := range r.Versions {
			if !yield(end.Row, end) {
				return
			}
		}
	}
}

func (r *DeletionRoot) Len() int {
	if r == nil {
		return 0
	}
	return len(r.Rows) + len(r.Versions)
}

func (r *DeletionRoot) Clone() *DeletionRoot {
	if r == nil {
		return nil
	}
	return &DeletionRoot{Rows: maps.Clone(r.Rows), Versions: maps.Clone(r.Versions)}
}

// Set mutates only an unpublished root. The first end for a row keeps the
// ordinary Rows slot; each colliding physical version has one Versions slot.
func (r *DeletionRoot) Set(row ids.RowID, end RowEnd) {
	end.Row = row
	if _, exists := r.Versions[end.Version]; exists {
		r.Versions[end.Version] = end
		return
	}
	if prior, ok := r.Rows[row]; !ok || prior.Version == end.Version {
		if r.Rows == nil {
			r.Rows = make(map[ids.RowID]RowEnd)
		}
		r.Rows[row] = end
		return
	}
	if r.Versions == nil {
		r.Versions = make(map[ids.VersionID]RowEnd)
	}
	r.Versions[end.Version] = end
}

// Delete removes only the selected physical end from an unpublished root.
func (r *DeletionRoot) Delete(row ids.RowID, version ids.VersionID) {
	if r == nil {
		return
	}
	if end, ok := r.Rows[row]; ok && end.Version == version {
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
	if end, ok := r.Versions[version]; ok && end.Row == row {
		delete(r.Versions, version)
	}
}
