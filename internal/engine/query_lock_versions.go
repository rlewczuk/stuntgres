package engine

// A materialized candidate owns version identity alongside physical provenance.
// Later explicit marks compare against the version this candidate actually
// carries, not an earlier leaf capture, so an already completed local recheck
// is not resurrected as a new replacement. Rank/cache identity remains separate.
func appendQueryVersions(dst []rowVersion, source *querySource, index int, locks *queryLockContext) []rowVersion {
	if index == absentQueryIndex {
		for range queryOccurrenceWidth(source) {
			dst = append(dst, rowVersion{})
		}
		return dst
	}
	if width := len(source.occurrences); width > 0 && (index+1)*width <= len(source.versions) && (source.versionValid == nil || index < len(source.versionValid) && source.versionValid[index]) {
		return append(dst, source.versions[index*width:(index+1)*width]...)
	}
	if source.input != nil {
		return appendQueryVersions(dst, source.input, source.inputIndex(index), locks)
	}
	if source.lineage != nil {
		physical := source.lineage.selection[index]
		return appendQueryVersions(dst, source.lineage.stage, physical, locks)
	}
	if source.singleton {
		return dst
	}
	if source.materialized {
		width := len(source.occurrences)
		for j, occurrence := range source.occurrences {
			leaf := locks.leaves[occurrence].source
			visible := source.provenance[index*width+j]
			if visible == absentQueryIndex {
				dst = append(dst, rowVersion{})
				continue
			}
			if leaf.anonymous != nil {
				dst = append(dst, rowVersion{Values: leaf.row(visible)})
			} else {
				dst = append(dst, leaf.versionAt(leaf.rawIndex(visible)))
			}
		}
		return dst
	}
	if source.anonymous != nil {
		return append(dst, rowVersion{Values: source.row(index)})
	}
	return append(dst, source.versionAt(source.rawIndex(index)))
}

type queryPair struct{ left, right int }

// Restore every participating view/cache identity for this frozen join
// candidate, including unmarked grouped siblings with no physical provenance.
func setQueryLineage(source *querySource, index int) {
	if source.input != nil {
		setQueryLineage(source.input, source.inputIndex(index))
		return
	}
	if lineage := source.lineage; lineage != nil {
		lineage.currentIndex = index
		if index == absentQueryIndex {
			lineage.currentOrder = nil
			setQueryLineage(lineage.stage, index)
			return
		}
		if lineage.cacheWidth != 0 {
			occurrence := index
			if lineage.cacheOccurrences != nil {
				occurrence = lineage.cacheOccurrences[index]
			}
			lineage.currentOrder = lineage.orderValues[occurrence*lineage.cacheWidth : (occurrence+1)*lineage.cacheWidth]
		}
		if lineage.selection != nil {
			index = lineage.selection[index]
		}
		setQueryLineage(lineage.stage, index)
		return
	}
	if len(source.invocationLineage) != 0 {
		setQueryInvocationLineage(source, index)
		return
	}
	if source.joinPairs != nil {
		if index == absentQueryIndex {
			setQueryLineage(source.joinLeft, index)
			setQueryLineage(source.joinRight, index)
			return
		}
		pair := source.joinPairs[index]
		setQueryLineage(source.joinLeft, pair.left)
		setQueryLineage(source.joinRight, pair.right)
	}
}
