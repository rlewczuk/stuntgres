package storage

import "stuntgres/internal/engine/ids"

// RetainDiscardedVersions transfers actual created private versions out of a
// disappearing owner root. Versions still owned by keep (including its genuine
// history) are not aborted. Committed ancestors remain owned by normal storage.
// No payload is copied, and neither old owner nor old retained nodes are changed.
func RetainDiscardedVersions(root *HeapPredecessor, discarded, keep []RowVersion) (*HeapPredecessor, bool) {
	if len(discarded) == 0 {
		return root, false
	}
	var known map[ids.VersionID]bool
	initialize := func() {
		known = make(map[ids.VersionID]bool)
		for prior := root; prior != nil; prior = prior.Next {
			known[prior.Version.Version] = true
		}
		for _, row := range keep {
			known[row.Version] = true
			for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
				known[prior.Version.Version] = true
			}
		}
	}
	result := root
	retain := func(row RowVersion) {
		if row.Version == 0 || row.Born != 0 {
			return
		}
		if known == nil {
			initialize()
		}
		if known[row.Version] {
			return
		}
		known[row.Version] = true
		row.PrivateHistory = nil
		row.IndexAdmission, row.IndexOutputs = nil, nil
		result = &HeapPredecessor{Version: row, Next: result}
	}
	for _, row := range discarded {
		retain(row)
		for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
			retain(prior.Version)
		}
	}
	return result, result != root
}

// PhysicalVersion resolves one exact genuine version across the canonical
// committed, historical, discarded and repaired roots. A committed-position hint
// is used only after checking VersionID; stale or unknown hints retain the exact
// fallback and never select a different head belonging to the same RowID.
func (s *Relation) PhysicalVersion(version ids.VersionID, hint int) (RowVersion, int, bool) {
	if version == 0 {
		return RowVersion{}, 0, false
	}
	if hint >= 0 && hint < len(s.Versions) && s.Versions[hint].Version == version {
		return s.Headers.Apply(s.Versions[hint]), hint, true
	}
	for i, row := range s.Versions {
		if row.Version == version {
			return s.Headers.Apply(row), i, true
		}
	}
	for prior := s.Retained; prior != nil; prior = prior.Next {
		if prior.Version.Version == version {
			return s.Headers.Apply(prior.Version), -1, true
		}
	}
	for prior := s.Discarded; prior != nil; prior = prior.Next {
		if prior.Version.Version == version {
			return s.Headers.Apply(prior.Version), -1, true
		}
	}
	if s.Headers != nil {
		for _, row := range s.Headers.Additional {
			if row.Version == version {
				return s.Headers.Apply(row), -1, true
			}
		}
	}
	return RowVersion{}, 0, false
}

func (s *Relation) RepairedVersions() []RowVersion {
	if s.Headers == nil {
		return nil
	}
	return s.Headers.Additional
}

// RowByVersion uses a physical-position hint without substituting a different
// version belonging to the same logical row.
func RowByVersion(rows []RowVersion, row ids.RowID, version ids.VersionID, hint int) (RowVersion, int, bool) {
	if hint >= 0 && hint < len(rows) && rows[hint].Row == row && rows[hint].Version == version {
		return rows[hint], hint, true
	}
	for i, candidate := range rows {
		if candidate.Row == row && candidate.Version == version {
			return candidate, i, true
		}
	}
	return RowVersion{}, 0, false
}

// MergeDiscardedVersions materializes a deferred immutable discard batch
// without a temporary row vector or tuple-payload copies.
func MergeDiscardedVersions(root, discarded *HeapPredecessor, keep []RowVersion) (*HeapPredecessor, bool) {
	if discarded == nil {
		return root, false
	}
	if root == nil && len(keep) == 0 {
		return discarded, true
	}
	known := make(map[ids.VersionID]bool)
	for prior := root; prior != nil; prior = prior.Next {
		known[prior.Version.Version] = true
	}
	for _, row := range keep {
		known[row.Version] = true
		for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
			known[prior.Version.Version] = true
		}
	}
	next := root
	for prior := discarded; prior != nil; prior = prior.Next {
		row := prior.Version
		if row.Version == 0 || row.Born != 0 || known[row.Version] {
			continue
		}
		known[row.Version] = true
		copy := *prior
		copy.Next = next
		next = &copy
	}
	return next, next != root
}

// ExcludeOwnedDiscardedVersions preserves the original root on a no-op and
// shares the suffix after the final removed version. Surviving owners include
// genuine private history, not merely their current logical heads.
func ExcludeOwnedDiscardedVersions(discarded *HeapPredecessor, keep []RowVersion) *HeapPredecessor {
	if discarded == nil || len(keep) == 0 {
		return discarded
	}
	var removed map[*HeapPredecessor]bool
	var last *HeapPredecessor
	for prior := discarded; prior != nil; prior = prior.Next {
		owned := false
		for _, row := range keep {
			if row.Version == prior.Version.Version {
				owned = true
				break
			}
			for history := row.PrivateHistory; history != nil; history = history.Next {
				if history.Version.Version == prior.Version.Version {
					owned = true
					break
				}
			}
			if owned {
				break
			}
		}
		if owned {
			if removed == nil {
				removed = make(map[*HeapPredecessor]bool)
			}
			removed[prior], last = true, prior
		}
	}
	if last == nil {
		return discarded
	}
	var result *HeapPredecessor
	tail := &result
	for prior := discarded; ; prior = prior.Next {
		if !removed[prior] {
			copy := *prior
			*tail = &copy
			tail = &copy.Next
		}
		if prior == last {
			*tail = prior.Next
			break
		}
	}
	return result
}
