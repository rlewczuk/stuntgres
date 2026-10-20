package engine

import (
	"context"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Each manifest entry carries a requested mode for expanded physical occurrence
// identities. Entries are immutable; frontier arbitration combines them in
// execution-private scratch, never in the shared capture leaf modes.
type queryPendingMark struct {
	occurrences []int
	mark        plan.RowMark
}

// Unlike the output pending-mark walk, dead qualification must inspect consumed
// input marks below blocking owners and expanded stored sources. A CTE scan is
// opaque: its body's frontier belongs to a later, actual invocation demand.
func queryHasMandatoryPhysicalFrontier(node plan.Node, inputs *querySource) bool {
	return queryHasPhysicalFrontier(node, inputs, false)
}

func queryHasPhysicalFrontier(node plan.Node, inputs *querySource, consuming bool) bool {
	switch n := node.(type) {
	case *plan.DerivedNode:
		if n.CTE != nil {
			return false
		}
	case *plan.WorktableScanNode:
		return false
	case *plan.SourceNode:
		return sourceHasPhysicalFrontier(querySourceAt(inputs, n.Occurrence), consuming)
	case *plan.SortNode, *plan.AggregateNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.SetNode, *plan.RecursiveUnionNode, *plan.LimitNode, *plan.WindowNode:
		consuming = true
	}
	left, right := queryInputs(node)
	return left != nil && queryHasPhysicalFrontier(left, inputs, consuming) ||
		right != nil && queryHasPhysicalFrontier(right, inputs, consuming)
}

func sourceHasPhysicalFrontier(source *querySource, consuming bool) bool {
	if source.input != nil {
		return sourceHasPhysicalFrontier(source.input, consuming)
	}
	if source.nestedRoot != nil {
		return queryHasPhysicalFrontier(source.nestedRoot, source.nestedInputs, consuming)
	}
	if !consuming || !source.locks.rowMarked() || source.singleton {
		return false
	}
	// Capture already merged the occurrence's local and inherited marks.
	// An unrelated marked leaf in the same context is not this branch's demand.
	if source.inherited.Mode != 0 {
		return true
	}
	if source.materialized {
		for _, occurrence := range source.occurrences {
			if source.locks.leaves[occurrence].mark.Mode != 0 {
				return true
			}
		}
		return false
	}
	return source.locks.leaves[source.occurrence].mark.Mode != 0
}

func pendingQueryMarks(node plan.Node, inputs *querySource, dst []queryPendingMark) []queryPendingMark {
	switch n := node.(type) {
	case *plan.DerivedNode, *plan.WorktableScanNode:
		// The derived projection frontier consumes its own child marks.
		return dst
	case *plan.ProjectNode:
		return pendingQueryMarks(n.Child, inputs, dst)
	case *plan.FilterNode:
		return pendingQueryMarks(n.Child, inputs, dst)
	case *plan.EvaluationNode:
		return pendingQueryMarks(n.Child, inputs, dst)
	case *plan.RowLockNode:
		dst = pendingQueryMarks(n.Child, inputs, dst)
		return appendPendingRowMarks(dst, n.Marks, inputs)
	case *plan.SourceNode:
		return pendingSourceMarks(querySourceAt(inputs, n.Occurrence), dst)
	case *plan.JoinNode:
		if n.Kind != parser.JoinInner {
			// Outer joins consume each local input mark before ON matching.
			// Do not lift it back onto matched or NULL-extended output.
			return dst
		}
		dst = pendingQueryMarks(n.Left, inputs, dst)
		return pendingQueryMarks(n.Right, inputs, dst)
	case *plan.SortNode, *plan.AggregateNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.SetNode, *plan.LimitNode:
		// These owners consume descendant pending marks. Own marks sit above
		// their Sort and therefore are discovered separately by plan.RowLockNode.
		return dst
	default:
		return dst
	}
}
func pendingSourceMarks(source *querySource, dst []queryPendingMark) []queryPendingMark {
	if source.input != nil {
		return pendingSourceMarks(source.input, dst)
	}
	if source.nestedRoot != nil {
		return pendingQueryMarks(source.nestedRoot, source.nestedInputs, dst)
	}
	return dst
}
func settlePendingQueryMarks(ctx context.Context, state *queryState, node plan.Node, child indexedStage) (indexedStage, error) {
	if len(child.pending) == 0 {
		return child, nil
	}
	return arbitrateViewQuery(ctx, state, node, child)
}
