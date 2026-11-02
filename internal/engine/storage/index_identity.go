package storage

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// IsMember requires the actual physical version. A row's other membership is
// never fallback authority for a repaired or subsequently updated sibling.
func (r *IndexRoot) IsMember(row ids.RowID, version ids.VersionID) bool {
	if prior, exists := r.Membership[row]; exists && prior == version {
		return true
	}
	prior, exists := r.MembershipVersions[version]
	return exists && prior == row
}

// SetMember records produced membership on an unpublished root. Only genuine
// collisions allocate a supplemental map; each membership has one owner.
func (r *IndexRoot) SetMember(row ids.RowID, version ids.VersionID) {
	if prior, exists := r.Membership[row]; !exists || prior == version {
		r.Membership[row] = version
		return
	}
	if r.MembershipVersions == nil {
		r.MembershipVersions = make(map[ids.VersionID]ids.RowID)
	}
	r.MembershipVersions[version] = row
}

func (r *IndexRoot) MembershipLen() int { return len(r.Membership) + len(r.MembershipVersions) }

func (r *IndexRoot) Members() iter.Seq2[ids.RowID, ids.VersionID] {
	return func(yield func(ids.RowID, ids.VersionID) bool) {
		for row, version := range r.Membership {
			if !yield(row, version) {
				return
			}
		}
		for version, row := range r.MembershipVersions {
			if !yield(row, version) {
				return
			}
		}
	}
}

// SetRowEntry indexes an actual entry ordinal on an unpublished root.
func (r *IndexRoot) SetRowEntry(at int) {
	entry := r.Entries[at]
	if prior, exists := r.RowEntries[entry.Row]; !exists || r.Entries[prior].Version == entry.Version {
		r.RowEntries[entry.Row] = at
		return
	}
	if r.RowEntryVersions == nil {
		r.RowEntryVersions = make(map[ids.VersionID]int)
	}
	r.RowEntryVersions[entry.Version] = at
}

func (r *IndexRoot) RowEntry(row ids.RowID, version ids.VersionID) (int, bool) {
	if at, exists := r.RowEntryVersions[version]; exists {
		return at, true
	}
	at, exists := r.RowEntries[row]
	if !exists {
		return 0, false
	}
	if at < 0 || at >= len(r.Entries) {
		return at, true
	}
	return at, r.Entries[at].Version == version
}

func (r *IndexRoot) RowEntryLen() int { return len(r.RowEntries) + len(r.RowEntryVersions) }

// RowEntryIdentities enumerates structural metadata for validation against the
// actual entry vector, preserving both ordinary row keys and collision versions.
func (r *IndexRoot) RowEntryIdentities() iter.Seq[IndexEntry] {
	return func(yield func(IndexEntry) bool) {
		for row, at := range r.RowEntries {
			entry := IndexEntry{Row: row, Position: at}
			if at >= 0 && at < len(r.Entries) {
				entry.Version = r.Entries[at].Version
			}
			if !yield(entry) {
				return
			}
		}
		for version, at := range r.RowEntryVersions {
			entry := IndexEntry{Version: version, Position: at}
			if at >= 0 && at < len(r.Entries) {
				entry.Row = r.Entries[at].Row
			}
			if !yield(entry) {
				return
			}
		}
	}
}

func (r *HashRoot) SetRowEntry(entry *HashEntry) {
	if prior, exists := r.RowEntries[entry.Row]; !exists || prior.Version == entry.Version {
		r.RowEntries[entry.Row] = entry
		return
	}
	if r.RowEntryVersions == nil {
		r.RowEntryVersions = make(map[ids.VersionID]*HashEntry)
	}
	r.RowEntryVersions[entry.Version] = entry
}

func (r *HashRoot) RowEntry(row ids.RowID, version ids.VersionID) *HashEntry {
	if entry := r.RowEntryVersions[version]; entry != nil {
		if entry.Row == row && entry.Version == version {
			return entry
		}
		return nil
	}
	entry := r.RowEntries[row]
	if entry != nil && entry.Row == row && entry.Version == version {
		return entry
	}
	return nil
}
