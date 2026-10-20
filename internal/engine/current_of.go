package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
)

// The named cursor owns its retained execution. Mutations borrow only a physical
// occurrence and then remap its stable RowID into their fresh command view.
func (s *StatementScope) positionedMutationRow(b analyze.Binding) (mutationRowIdentity, error) {
	c := payloadOf(s.op.session).cursors.entries[b.CurrentOf]
	if c == nil || (!c.promoted && c.tx != s.txRef) {
		return mutationRowIdentity{}, missingSQLCursor(b.CurrentOf)
	}
	if c.epoch != s.txRef.Epoch() {
		return mutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	if c.position == cursorFailed || c.root == nil {
		return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" is held from a previous transaction", b.CurrentOf))
	}
	occurrence := -1
	marked := false
	if c.locks != nil {
		for i, leaf := range c.locks.Leaves {
			if leaf.Mark.Mode == 0 {
				continue
			}
			marked = true
			if leaf.Leaf.Ref != b.Ref {
				continue
			}
			if occurrence != -1 {
				return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" has multiple FOR UPDATE/SHARE references to table \"%s\"", b.CurrentOf, b.Relation.Name()))
			}
			occurrence = i
		}
	}
	if marked {
		if occurrence == -1 {
			return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" does not have a FOR UPDATE/SHARE reference to table \"%s\"", b.CurrentOf, b.Relation.Name()))
		}
	} else {
		scan := cursorSimpleScan(c.root, c.source)
		if scan == nil || scan.Ref != b.Ref {
			return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" is not a simply updatable scan of table \"%s\"", b.CurrentOf, b.Relation.Name()))
		}
		occurrence = scan.Occurrence
	}
	if c.position != cursorOn {
		return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" is not positioned on a row", b.CurrentOf))
	}
	if c.locks == nil || occurrence < 0 || occurrence >= len(c.locks.Leaves) {
		return mutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	physical := cursorOccurrencePhysicalRow(c.currentSource, c.currentIndex, occurrence, c.locks)
	if physical.Version.Row == 0 {
		return mutationRowIdentity{}, nil
	}
	storage := s.mutationStorage(physical.Leaf.Ref)
	if storage == nil {
		// ONLY excludes a positioned descendant, without changing its cursor.
		return mutationRowIdentity{}, nil
	}
	if storage != physical.Leaf.Storage {
		return mutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	return mutationRowIdentity{physical.Leaf.Ref, physical.Version.Row}, nil
}

// Only occurrence-preserving nodes may expose an unlocked physical scan.
// A real sort/aggregate/join/set frontier is not a simply updatable scan.
func cursorSimpleScan(node plan.Node, inputs *exec.Source) *exec.Source {
	switch n := node.(type) {
	case *plan.ProjectNode:
		return cursorSimpleScan(n.Child, inputs)
	case *plan.FilterNode:
		return cursorSimpleScan(n.Child, inputs)
	case *plan.EvaluationNode:
		return cursorSimpleScan(n.Child, inputs)
	case *plan.LimitNode:
		return cursorSimpleScan(n.Child, inputs)
	case *plan.DerivedNode:
		return cursorSimpleScan(n.Query.Root, inputs)
	case *plan.SourceNode:
		source := exec.QuerySourceAt(inputs, n.Occurrence)
		for source.Input != nil {
			source = source.Input
		}
		if source.NestedRoot != nil {
			return cursorSimpleScan(source.NestedRoot, source.NestedInputs)
		}
		if source.View == nil || source.Materialized || source.Anonymous != nil || source.Singleton {
			return nil
		}
		return source
	default:
		return nil
	}
}

// Resolve one occurrence directly, without allocating the all-leaf vector used
// by locking rechecks. Cached effective versions win over DECLARE-era versions.
func cursorOccurrenceVersion(source *exec.Source, index, occurrence int, locks *exec.LockContext) storage.RowVersion {
	if source == nil || index == exec.AbsentQueryIndex {
		return storage.RowVersion{}
	}
	width := len(source.Occurrences)
	if width > 0 && (index+1)*width <= len(source.Versions) && (source.VersionValid == nil || index < len(source.VersionValid) && source.VersionValid[index]) {
		for j, id := range source.Occurrences {
			if id == occurrence {
				return source.Versions[index*width+j]
			}
		}
		return storage.RowVersion{}
	}
	if source.Input != nil {
		return cursorOccurrenceVersion(source.Input, index, occurrence, locks)
	}
	if source.Lineage != nil {
		return cursorOccurrenceVersion(source.Lineage.Stage, source.Lineage.Selection[index], occurrence, locks)
	}
	if source.Materialized {
		for j, id := range source.Occurrences {
			if id != occurrence {
				continue
			}
			visible := source.Provenance[index*width+j]
			if visible == exec.AbsentQueryIndex {
				return storage.RowVersion{}
			}
			leaf := locks.Leaves[id].Source
			if leaf.Anonymous != nil {
				return storage.RowVersion{}
			}
			return leaf.VersionAt(leaf.RawIndex(visible))
		}
		return storage.RowVersion{}
	}
	if source.Singleton || source.Anonymous != nil || source.Occurrence != occurrence {
		return storage.RowVersion{}
	}
	return source.VersionAt(source.RawIndex(index))
}

func cursorOccurrencePhysicalRow(source *exec.Source, index, occurrence int, locks *exec.LockContext) exec.QueryPhysicalRow {
	if source == nil || index == exec.AbsentQueryIndex {
		return exec.QueryPhysicalRow{}
	}
	if source.Input != nil {
		return cursorOccurrencePhysicalRow(source.Input, source.InputIndex(index), occurrence, locks)
	}
	if source.Lineage != nil {
		if source.Lineage.Selection != nil {
			index = source.Lineage.Selection[index]
		}
		return cursorOccurrencePhysicalRow(source.Lineage.Stage, index, occurrence, locks)
	}
	visible := index
	if source.Materialized {
		visible = exec.AbsentQueryIndex
		for j, id := range source.Occurrences {
			if id == occurrence {
				visible = source.Provenance[index*len(source.Occurrences)+j]
				break
			}
		}
	} else if source.Occurrence != occurrence {
		return exec.QueryPhysicalRow{}
	}
	if visible == exec.AbsentQueryIndex || occurrence < 0 || occurrence >= len(locks.Leaves) {
		return exec.QueryPhysicalRow{}
	}
	leaf := locks.Leaves[occurrence]
	version := cursorOccurrenceVersion(source, index, occurrence, locks)
	return leaf.Source.PhysicalRow(visible, leaf.Leaf, version)
}
