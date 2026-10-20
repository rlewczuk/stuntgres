package engine

import "stuntgres/internal/engine/storage"

// A materialized candidate owns version identity alongside physical provenance.
// Later explicit marks compare against the version this candidate actually
// carries, not an earlier leaf capture, so an already completed local recheck
// is not resurrected as a new replacement. Rank/cache identity remains separate.
func AppendQueryVersions(dst []storage.RowVersion, source *querySource, index int, locks *queryLockContext) []storage.RowVersion {
	if index == AbsentQueryIndex {
		for range queryOccurrenceWidth(source) {
			dst = append(dst, storage.RowVersion{})
		}
		return dst
	}
	if width := len(source.Occurrences); width > 0 && (index+1)*width <= len(source.Versions) && (source.VersionValid == nil || index < len(source.VersionValid) && source.VersionValid[index]) {
		return append(dst, source.Versions[index*width:(index+1)*width]...)
	}
	if source.Input != nil {
		return AppendQueryVersions(dst, source.Input, source.InputIndex(index), locks)
	}
	if source.Lineage != nil {
		physical := source.Lineage.Selection[index]
		return AppendQueryVersions(dst, source.Lineage.Stage, physical, locks)
	}
	if source.Singleton {
		return dst
	}
	if source.Materialized {
		width := len(source.Occurrences)
		for j, occurrence := range source.Occurrences {
			leaf := locks.Leaves[occurrence].Source
			visible := source.Provenance[index*width+j]
			if visible == AbsentQueryIndex {
				dst = append(dst, storage.RowVersion{})
				continue
			}
			if leaf.Anonymous != nil {
				dst = append(dst, storage.RowVersion{Values: leaf.Row(visible)})
			} else {
				dst = append(dst, leaf.VersionAt(leaf.RawIndex(visible)))
			}
		}
		return dst
	}
	if source.Anonymous != nil {
		return append(dst, storage.RowVersion{Values: source.Row(index)})
	}
	return append(dst, source.VersionAt(source.RawIndex(index)))
}

type queryPair struct{ Left, Right int }

// Restore every participating view/cache identity for this frozen join
// candidate, including unmarked grouped siblings with no physical provenance.
func SetQueryLineage(source *querySource, index int) {
	if source.Input != nil {
		SetQueryLineage(source.Input, source.InputIndex(index))
		return
	}
	if lineage := source.Lineage; lineage != nil {
		lineage.CurrentIndex = index
		if index == AbsentQueryIndex {
			lineage.CurrentOrder = nil
			SetQueryLineage(lineage.Stage, index)
			return
		}
		if lineage.CacheWidth != 0 {
			occurrence := index
			if lineage.cacheOccurrences != nil {
				occurrence = lineage.cacheOccurrences[index]
			}
			lineage.CurrentOrder = lineage.orderValues[occurrence*lineage.CacheWidth : (occurrence+1)*lineage.CacheWidth]
		}
		if lineage.Selection != nil {
			index = lineage.Selection[index]
		}
		SetQueryLineage(lineage.Stage, index)
		return
	}
	if len(source.invocationLineage) != 0 {
		setQueryInvocationLineage(source, index)
		return
	}
	if source.JoinPairs != nil {
		if index == AbsentQueryIndex {
			SetQueryLineage(source.JoinLeft, index)
			SetQueryLineage(source.JoinRight, index)
			return
		}
		pair := source.JoinPairs[index]
		SetQueryLineage(source.JoinLeft, pair.Left)
		SetQueryLineage(source.JoinRight, pair.Right)
	}
}
