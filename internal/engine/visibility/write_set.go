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
		rows := make(map[ids.RowID]DeleteCandidate, len(old.Rows)+len(root.Rows))
		for id, candidate := range old.Rows {
			rows[id] = candidate
		}
		for id, candidate := range root.Rows {
			rows[id] = candidate
		}
		next[a] = &PrivateDeletes{Rows: rows}
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
	if len(stage) == 1 {
		for i, row := range old {
			if row.Row == stage[0].Row {
				next[i] = stage[0]
				return next
			}
		}
		return append(next, stage[0])
	}
	slots := make(map[ids.RowID]int, len(old))
	for i, row := range old {
		slots[row.Row] = i
	}
	for _, row := range stage {
		if i, ok := slots[row.Row]; ok {
			next[i] = row
		} else {
			slots[row.Row] = len(next)
			next = append(next, row)
		}
	}
	return next
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
