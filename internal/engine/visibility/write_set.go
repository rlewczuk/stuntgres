package visibility

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Write-set merges for WriteStage, rule and SQL routine scratch restores and
// transaction finish. MergeDeletions overlays staged private deletions on the
// earlier set without mutating either input.
func MergeDeletions(previous, staged map[ids.ObjectAddress]*PrivateDeletes) map[ids.ObjectAddress]*PrivateDeletes {
	if len(staged) == 0 {
		return previous
	}
	next := make(map[ids.ObjectAddress]*PrivateDeletes, len(previous)+len(staged))
	for a, root := range previous {
		next[a] = root
	}
	for a, root := range staged {
		old := previous[a]
		if old == nil {
			next[a] = root
			continue
		}
		merged := old.Clone()
		for _, candidate := range root.All() {
			merged.Set(candidate)
		}
		next[a] = merged
	}
	return next
}

// A replacement merge never changes a prefix retained by an earlier view.
// Insert-only commands retain the existing amortized append path.
func MergePrivateRows(old, stage []storage.RowVersion, replacing bool) []storage.RowVersion {
	if len(old) == 0 {
		return stage
	}
	if !replacing {
		return append(old, stage...)
	}
	next := make([]storage.RowVersion, len(old), len(old)+len(stage))
	copy(next, old)
	var slots map[ids.VersionID]int
	if len(stage) > 1 {
		slots = make(map[ids.VersionID]int, len(old)+len(stage))
		for i, row := range old {
			slots[row.Version] = i
		}
	}
	for _, row := range stage {
		slot := privateReplacementSlot(next, row, slots)
		if slot >= 0 {
			if slots != nil {
				delete(slots, next[slot].Version)
			}
			next[slot] = row
		} else {
			slot = len(next)
			next = append(next, row)
		}
		if slots != nil {
			slots[row.Version] = slot
		}
	}
	return next
}

// Resolve the nearest actual predecessor still present in the earlier owner
// root. RowID alone cannot distinguish independently visible repaired siblings.
func privateReplacementSlot(rows []storage.RowVersion, next storage.RowVersion, slots map[ids.VersionID]int) int {
	if slots != nil {
		find := func(version ids.VersionID) int {
			if i, ok := slots[version]; ok && rows[i].Row == next.Row {
				return i
			}
			return -1
		}
		if i := find(next.Version); i >= 0 {
			return i
		}
		for prior := next.PrivateHistory; prior != nil; prior = prior.Next {
			if i := find(prior.Version.Version); i >= 0 {
				return i
			}
		}
		if next.Base != 0 {
			return find(next.Base)
		}
		return -1
	}
	best, distance := -1, int(^uint(0)>>1)
	for i, row := range rows {
		if row.Row != next.Row {
			continue
		}
		if row.Version == next.Version {
			return i
		}
		at := 0
		for prior := next.PrivateHistory; prior != nil; prior = prior.Next {
			if prior.Version.Version == row.Version && at < distance {
				best, distance = i, at
				break
			}
			at++
		}
		if next.PrivateHistory == nil && next.Base != 0 && row.Version == next.Base {
			return i
		}
	}
	return best
}

// Prepare only vector headers/capacity. Tail writes follow the final preflight.
func PrepareOwnWrites(previous, staged map[ids.ObjectAddress][]storage.RowVersion, replacing bool) map[ids.ObjectAddress][]storage.RowVersion {
	if len(staged) == 0 {
		return previous
	}
	next := make(map[ids.ObjectAddress][]storage.RowVersion, len(previous)+len(staged))
	for a, rows := range previous {
		next[a] = rows
	}
	for a, rows := range staged {
		old := previous[a]
		if replacing {
			next[a] = MergePrivateRows(old, rows, true)
			continue
		}
		if len(old) == 0 {
			next[a] = rows
			continue
		}
		size := len(old) + len(rows)
		if size <= cap(old) {
			next[a] = old[:size]
			continue
		}
		capacity := max(size, cap(old)*2)
		dst := make([]storage.RowVersion, size, capacity)
		copy(dst, old)
		next[a] = dst
	}
	return next
}
