package storage

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
)

// MapHeapHeaderRows transports only the actual repaired payloads. Header and
// slot authority stays immutable and shared; a no-op retains the exact root.
func MapHeapHeaderRows(root *HeapHeaderRoot, mapper func(RowVersion) (RowVersion, bool, error)) (*HeapHeaderRoot, error) {
	if root == nil {
		return nil, nil
	}
	var rows []RowVersion
	for i, row := range root.Additional {
		mapped, changed, err := mapper(row)
		if err != nil {
			return nil, err
		}
		if changed {
			if rows == nil {
				rows = append([]RowVersion(nil), root.Additional...)
			}
			rows[i] = mapped
		}
	}
	if rows == nil {
		return root, nil
	}
	next := *root
	next.Additional = rows
	return &next, nil
}

// EffectiveHeapRows applies captured exact headers, copying the vector only
// when a header changes. Immutable values and untouched captures remain shared.
func EffectiveHeapRows(rows []RowVersion, headers *HeapHeaderRoot) []RowVersion {
	if headers == nil {
		return rows
	}
	var next []RowVersion
	for i, row := range rows {
		if _, ok := headers.Versions.Get(row.Version); !ok {
			continue
		}
		mapped := headers.Apply(row)
		if mapped.HeapKilled == row.HeapKilled && mapped.DeleteFullXID == row.DeleteFullXID && mapped.Frozen == row.Frozen && mapped.ForcedFrozen == row.ForcedFrozen && mapped.RepairStamp == row.RepairStamp && mapped.ForcedEnd == row.ForcedEnd {
			continue
		}
		if next == nil {
			next = append([]RowVersion(nil), rows...)
		}
		next[i] = mapped
	}
	if next == nil {
		return rows
	}
	return next
}

// VisibleHeapVersions captures current SQL data, not history or physical slot
// identities for replay. The caller holds the storage owner lock. Exact version
// deduplication permits repaired siblings sharing a logical RowID.
func VisibleHeapVersions(store *Relation, frontier ids.CommitSeq) []RowVersion {
	live := func(row RowVersion) bool {
		if row.HeapKilled {
			return false
		}
		if row.ForcedFrozen {
			return row.ForcedEnd == 0 || row.ForcedEnd > frontier
		}
		return row.Born != 0 && row.Born <= frontier && !vacuumEnded(row, store.Ends, frontier)
	}
	rows := EffectiveHeapRows(store.Versions, store.Headers)
	result := rows
	filtered := false
	for i, row := range rows {
		if !live(row) {
			if !filtered {
				result = make([]RowVersion, i, len(rows))
				copy(result, rows[:i])
				filtered = true
			}
		} else if filtered {
			result = append(result, row)
		}
	}
	if store.Headers == nil || len(store.Headers.Additional) == 0 {
		return result
	}
	seen := make(map[ids.VersionID]bool, len(result)+len(store.Headers.Additional))
	for _, row := range result {
		seen[row.Version] = true
	}
	owned := filtered
	for _, row := range store.Headers.Additional {
		if seen[row.Version] {
			continue
		}
		row = store.Headers.Apply(row)
		if !live(row) {
			continue
		}
		if !owned {
			next := make([]RowVersion, len(result), len(result)+len(store.Headers.Additional))
			copy(next, result)
			result, owned = next, true
		}
		result = append(result, row)
		seen[row.Version] = true
	}
	return result
}

// MaintainHeapHistory prunes only actual captured versions under the existing
// retirement gate. Retired slot records own allocation/disposition, not payloads.
// A repaired live historical version remains ordinary live SQL data.
func MaintainHeapHistory(ctx context.Context, c VacuumCandidate, source Relation, frontier ids.CommitSeq, retire, indexed, keepDead bool) (VacuumCandidate, error) {
	c.Headers, c.Discarded = source.Headers, source.Discarded
	var next *HeapHeaderRoot
	var retired map[ids.VersionID]bool
	retireSlot := func(row RowVersion, redirect ids.VersionID) {
		if next == nil {
			next = &HeapHeaderRoot{}
			if c.Headers != nil {
				next.Versions = c.Headers.Versions
				next.Stamp, next.Additional = c.Headers.Stamp, c.Headers.Additional
			}
		}
		header := HeapHeader{Slot: HeapSlotUnused}
		if redirect != 0 {
			header.Slot, header.Redirect = HeapSlotRedirect, redirect
		} else if indexed && keepDead {
			header.Slot = HeapSlotDead
		}
		next.Versions = next.Versions.Set(row.Version, header)
		if retired == nil {
			retired = make(map[ids.VersionID]bool)
		}
		retired[row.Version] = true
	}
	for _, row := range source.Versions {
		if err := ctx.Err(); err != nil {
			return VacuumCandidate{}, err
		}
		if retire && vacuumEnded(source.Headers.Apply(row), source.Ends, frontier) {
			retireSlot(row, 0)
		}
	}
	var seen map[ids.VersionID]bool
	if source.Retained != nil || source.Discarded != nil || source.Headers != nil && len(source.Headers.Additional) != 0 {
		seen = make(map[ids.VersionID]bool, len(source.Versions))
		for _, row := range source.Versions {
			seen[row.Version] = true
		}
	}
	var survivors map[ids.VersionID]RowVersion
	var resolved map[ids.VersionID]ids.VersionID
	var path []ids.VersionID
	if retire && !indexed && source.Retained != nil && source.Headers != nil {
		survivors = make(map[ids.VersionID]RowVersion, len(c.Versions))
		for _, row := range c.Versions {
			survivors[row.Version] = row
		}
		for _, row := range source.Headers.Additional {
			row = source.Headers.Apply(row)
			if !row.HeapKilled && row.ForcedFrozen && (row.ForcedEnd == 0 || row.ForcedEnd > frontier) {
				survivors[row.Version] = row
			}
		}
		resolved = make(map[ids.VersionID]ids.VersionID)
	}
	resolve := func(version ids.VersionID) ids.VersionID {
		if resolved == nil {
			return 0
		}
		path = path[:0]
		current, target := version, ids.VersionID(0)
		for {
			if known, ok := resolved[current]; ok {
				target = known
				break
			}
			path = append(path, current)
			header := source.Headers.Header(current)
			if header.Slot != HeapSlotNormal || header.Successor <= current {
				break
			}
			current = header.Successor
			if _, ok := survivors[current]; ok && source.Headers.Header(current).Slot == HeapSlotNormal {
				target = current
				break
			}
		}
		for _, prior := range path {
			resolved[prior] = target
		}
		return target
	}
	process := func(root *HeapPredecessor, committed bool) (*HeapPredecessor, error) {
		var removed map[*HeapPredecessor]bool
		for prior := root; prior != nil; prior = prior.Next {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			row := source.Headers.Apply(prior.Version)
			if seen[row.Version] {
				continue
			}
			seen[row.Version] = true
			live := !row.HeapKilled && row.ForcedFrozen && (row.ForcedEnd == 0 || row.ForcedEnd > frontier)
			if live {
				c.Live++
				continue
			}
			if committed && (prior.End == 0 || prior.End > frontier || prior.Version.Born == 0) {
				return nil, fmt.Errorf("stuntgres: invalid committed heap predecessor")
			}
			if !retire {
				c.Dead++
				continue
			}
			var redirect ids.VersionID
			if successor, ok := survivors[resolve(row.Version)]; committed && ok && successor.Row == row.Row {
				a, _, _ := LogicalTID(row.Version)
				b, _, _ := LogicalTID(successor.Version)
				if a == b {
					redirect = successor.Version
				}
			}
			retireSlot(row, redirect)
			c.Removed++
			if removed == nil {
				removed = make(map[*HeapPredecessor]bool)
			}
			removed[prior] = true
		}
		if len(removed) == 0 {
			return root, nil
		}
		var result *HeapPredecessor
		tail := &result
		for prior := root; prior != nil; prior = prior.Next {
			if removed[prior] {
				continue
			}
			copy := *prior
			copy.Next = nil
			*tail = &copy
			tail = &copy.Next
		}
		return result, nil
	}
	var err error
	c.Retained, err = process(source.Retained, true)
	if err != nil {
		return VacuumCandidate{}, err
	}
	c.Discarded, err = process(source.Discarded, false)
	if err != nil {
		return VacuumCandidate{}, err
	}
	if source.Headers != nil {
		if retire && !keepDead {
			for version, header := range source.Headers.Versions.All() {
				if header.Slot == HeapSlotDead {
					retireSlot(RowVersion{Version: version}, 0)
				}
			}
		}
		for _, original := range source.Headers.Additional {
			if err := ctx.Err(); err != nil {
				return VacuumCandidate{}, err
			}
			if seen[original.Version] {
				continue
			}
			seen[original.Version] = true
			row := source.Headers.Apply(original)
			if !row.HeapKilled && row.ForcedFrozen && (row.ForcedEnd == 0 || row.ForcedEnd > frontier) {
				c.Live++
			} else if retire {
				retireSlot(row, 0)
				c.Removed++
			} else {
				c.Dead++
			}
		}
	}
	if next != nil {
		if len(next.Additional) != 0 {
			rows := make([]RowVersion, 0, len(next.Additional))
			for _, row := range next.Additional {
				if !retired[row.Version] {
					rows = append(rows, row)
				}
			}
			next.Additional = rows
		}
		c.Headers = next
	}
	return c, ctx.Err()
}
