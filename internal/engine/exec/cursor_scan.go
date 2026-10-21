package exec

import (
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
)

// Only occurrence-preserving nodes may expose an unlocked physical scan.
// A real sort/aggregate/join/set frontier is not a simply updatable scan.
func CursorSimpleScan(node plan.Node, inputs *Source) *Source {
	switch n := node.(type) {
	case *plan.ProjectNode:
		return CursorSimpleScan(n.Child, inputs)
	case *plan.FilterNode:
		return CursorSimpleScan(n.Child, inputs)
	case *plan.EvaluationNode:
		return CursorSimpleScan(n.Child, inputs)
	case *plan.LimitNode:
		return CursorSimpleScan(n.Child, inputs)
	case *plan.DerivedNode:
		return CursorSimpleScan(n.Query.Root, inputs)
	case *plan.SourceNode:
		source := QuerySourceAt(inputs, n.Occurrence)
		for source.Input != nil {
			source = source.Input
		}
		if source.NestedRoot != nil {
			return CursorSimpleScan(source.NestedRoot, source.NestedInputs)
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
func CursorOccurrenceVersion(source *Source, index, occurrence int, locks *LockContext) storage.RowVersion {
	if source == nil || index == AbsentQueryIndex {
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
		return CursorOccurrenceVersion(source.Input, index, occurrence, locks)
	}
	if source.Lineage != nil {
		return CursorOccurrenceVersion(source.Lineage.Stage, source.Lineage.Selection[index], occurrence, locks)
	}
	if source.Materialized {
		for j, id := range source.Occurrences {
			if id != occurrence {
				continue
			}
			visible := source.Provenance[index*width+j]
			if visible == AbsentQueryIndex {
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

func CursorOccurrencePhysicalRow(source *Source, index, occurrence int, locks *LockContext) QueryPhysicalRow {
	if source == nil || index == AbsentQueryIndex {
		return QueryPhysicalRow{}
	}
	if source.Input != nil {
		return CursorOccurrencePhysicalRow(source.Input, source.InputIndex(index), occurrence, locks)
	}
	if source.Lineage != nil {
		if source.Lineage.Selection != nil {
			index = source.Lineage.Selection[index]
		}
		return CursorOccurrencePhysicalRow(source.Lineage.Stage, index, occurrence, locks)
	}
	visible := index
	if source.Materialized {
		visible = AbsentQueryIndex
		for j, id := range source.Occurrences {
			if id == occurrence {
				visible = source.Provenance[index*len(source.Occurrences)+j]
				break
			}
		}
	} else if source.Occurrence != occurrence {
		return QueryPhysicalRow{}
	}
	if visible == AbsentQueryIndex || occurrence < 0 || occurrence >= len(locks.Leaves) {
		return QueryPhysicalRow{}
	}
	leaf := locks.Leaves[occurrence]
	version := CursorOccurrenceVersion(source, index, occurrence, locks)
	return leaf.Source.PhysicalRow(visible, leaf.Leaf, version)
}
