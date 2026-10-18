package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

// The named cursor owns its retained execution. Mutations borrow only a physical
// occurrence and then remap its stable RowID into their fresh command view.
func (s *StatementScope) positionedMutationRow(b binding) (mutationRowIdentity, error) {
	c := s.op.session.cursors.entries[b.currentOf]
	if c == nil || (!c.promoted && c.tx != s.txRef) {
		return mutationRowIdentity{}, missingSQLCursor(b.currentOf)
	}
	if c.epoch != s.txRef.Epoch() {
		return mutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	if c.position == cursorFailed || c.root == nil {
		return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" is held from a previous transaction", b.currentOf))
	}
	occurrence := -1
	marked := false
	if c.locks != nil {
		for i, leaf := range c.locks.leaves {
			if leaf.mark.mode == 0 {
				continue
			}
			marked = true
			if leaf.leaf.ref != b.ref {
				continue
			}
			if occurrence != -1 {
				return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" has multiple FOR UPDATE/SHARE references to table \"%s\"", b.currentOf, b.relation.name))
			}
			occurrence = i
		}
	}
	if marked {
		if occurrence == -1 {
			return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" does not have a FOR UPDATE/SHARE reference to table \"%s\"", b.currentOf, b.relation.name))
		}
	} else {
		scan := cursorSimpleScan(c.root, c.source)
		if scan == nil || scan.ref != b.ref {
			return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" is not a simply updatable scan of table \"%s\"", b.currentOf, b.relation.name))
		}
		occurrence = scan.occurrence
	}
	if c.position != cursorOn {
		return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" is not positioned on a row", b.currentOf))
	}
	if c.locks == nil || occurrence < 0 || occurrence >= len(c.locks.leaves) {
		return mutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	physical := cursorOccurrencePhysicalRow(c.currentSource, c.currentIndex, occurrence, c.locks)
	if physical.version.row == 0 {
		return mutationRowIdentity{}, nil
	}
	storage := s.mutationStorage(physical.leaf.ref)
	if storage == nil {
		// ONLY excludes a positioned descendant, without changing its cursor.
		return mutationRowIdentity{}, nil
	}
	if storage != physical.leaf.storage {
		return mutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	return mutationRowIdentity{physical.leaf.ref, physical.version.row}, nil
}

// Only occurrence-preserving nodes may expose an unlocked physical scan.
// A real sort/aggregate/join/set frontier is not a simply updatable scan.
func cursorSimpleScan(node LogicalNode, inputs *querySource) *querySource {
	switch n := node.(type) {
	case *projectNode:
		return cursorSimpleScan(n.child, inputs)
	case *filterNode:
		return cursorSimpleScan(n.child, inputs)
	case *evaluationNode:
		return cursorSimpleScan(n.child, inputs)
	case *limitNode:
		return cursorSimpleScan(n.child, inputs)
	case *derivedNode:
		return cursorSimpleScan(n.query.Root, inputs)
	case *sourceNode:
		source := querySourceAt(inputs, n.occurrence)
		for source.input != nil {
			source = source.input
		}
		if source.nestedRoot != nil {
			return cursorSimpleScan(source.nestedRoot, source.nestedInputs)
		}
		if source.view == nil || source.materialized || source.anonymous != nil || source.singleton {
			return nil
		}
		return source
	default:
		return nil
	}
}

// Resolve one occurrence directly, without allocating the all-leaf vector used
// by locking rechecks. Cached effective versions win over DECLARE-era versions.
func cursorOccurrenceVersion(source *querySource, index, occurrence int, locks *queryLockContext) rowVersion {
	if source == nil || index == absentQueryIndex {
		return rowVersion{}
	}
	width := len(source.occurrences)
	if width > 0 && (index+1)*width <= len(source.versions) && (source.versionValid == nil || index < len(source.versionValid) && source.versionValid[index]) {
		for j, id := range source.occurrences {
			if id == occurrence {
				return source.versions[index*width+j]
			}
		}
		return rowVersion{}
	}
	if source.input != nil {
		return cursorOccurrenceVersion(source.input, index, occurrence, locks)
	}
	if source.lineage != nil {
		return cursorOccurrenceVersion(source.lineage.stage, source.lineage.selection[index], occurrence, locks)
	}
	if source.materialized {
		for j, id := range source.occurrences {
			if id != occurrence {
				continue
			}
			visible := source.provenance[index*width+j]
			if visible == absentQueryIndex {
				return rowVersion{}
			}
			leaf := locks.leaves[id].source
			if leaf.anonymous != nil {
				return rowVersion{}
			}
			return leaf.versionAt(leaf.rawIndex(visible))
		}
		return rowVersion{}
	}
	if source.singleton || source.anonymous != nil || source.occurrence != occurrence {
		return rowVersion{}
	}
	return source.versionAt(source.rawIndex(index))
}

func cursorOccurrencePhysicalRow(source *querySource, index, occurrence int, locks *queryLockContext) queryPhysicalRow {
	if source == nil || index == absentQueryIndex {
		return queryPhysicalRow{}
	}
	if source.input != nil {
		return cursorOccurrencePhysicalRow(source.input, source.inputIndex(index), occurrence, locks)
	}
	if source.lineage != nil {
		if source.lineage.selection != nil {
			index = source.lineage.selection[index]
		}
		return cursorOccurrencePhysicalRow(source.lineage.stage, index, occurrence, locks)
	}
	visible := index
	if source.materialized {
		visible = absentQueryIndex
		for j, id := range source.occurrences {
			if id == occurrence {
				visible = source.provenance[index*len(source.occurrences)+j]
				break
			}
		}
	} else if source.occurrence != occurrence {
		return queryPhysicalRow{}
	}
	if visible == absentQueryIndex || occurrence < 0 || occurrence >= len(locks.leaves) {
		return queryPhysicalRow{}
	}
	leaf := locks.leaves[occurrence]
	version := cursorOccurrenceVersion(source, index, occurrence, locks)
	return leaf.source.physicalRow(visible, leaf.leaf, version)
}
