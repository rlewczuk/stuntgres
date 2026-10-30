package exec

import (
	"context"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Each manifest entry carries a requested mode for expanded physical occurrence
// identities. Entries are immutable; frontier arbitration combines them in
// execution-private scratch, never in the shared capture leaf modes.
type queryPendingMark struct {
	Occurrences []int
	Mark        plan.RowMark
}

// Unlike the output pending-mark walk, dead qualification must inspect consumed
// input marks below blocking owners and expanded stored sources. A CTE scan is
// opaque: its body's frontier belongs to a later, actual invocation demand.
func QueryHasMandatoryPhysicalFrontier(node plan.Node, inputs *Source) bool {
	return queryHasPhysicalFrontier(node, inputs, false)
}

func queryHasPhysicalFrontier(node plan.Node, inputs *Source, consuming bool) bool {
	switch n := node.(type) {
	case *plan.DerivedNode:
		if n.CTE != nil {
			return false
		}
	case *plan.WorktableScanNode:
		return false
	case *plan.SourceNode:
		return sourceHasPhysicalFrontier(QuerySourceAt(inputs, n.Occurrence), consuming)
	case *plan.SortNode, *plan.AggregateNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.SetNode, *plan.RecursiveUnionNode, *plan.LimitNode, *plan.WindowNode:
		consuming = true
	}
	left, right := plan.QueryInputs(node)
	return left != nil && queryHasPhysicalFrontier(left, inputs, consuming) ||
		right != nil && queryHasPhysicalFrontier(right, inputs, consuming)
}

func sourceHasPhysicalFrontier(source *Source, consuming bool) bool {
	if source.Input != nil {
		return sourceHasPhysicalFrontier(source.Input, consuming)
	}
	if source.NestedRoot != nil {
		return queryHasPhysicalFrontier(source.NestedRoot, source.NestedInputs, consuming)
	}
	if !consuming || !source.Locks.RowMarked() || source.Singleton {
		return false
	}
	// Capture already merged the occurrence's local and inherited marks.
	// An unrelated marked leaf in the same context is not this branch's demand.
	if source.Inherited.Mode != 0 {
		return true
	}
	if source.Materialized {
		for _, occurrence := range source.Occurrences {
			if source.Locks.Leaves[occurrence].Mark.Mode != 0 {
				return true
			}
		}
		return false
	}
	return source.Locks.Leaves[source.Occurrence].Mark.Mode != 0
}

func PendingQueryMarks(node plan.Node, inputs *Source, dst []queryPendingMark) []queryPendingMark {
	switch n := node.(type) {
	case *plan.DerivedNode, *plan.WorktableScanNode:
		// The derived projection frontier consumes its own child marks.
		return dst
	case *plan.ProjectNode:
		return PendingQueryMarks(n.Child, inputs, dst)
	case *plan.FilterNode:
		return PendingQueryMarks(n.Child, inputs, dst)
	case *plan.EvaluationNode:
		return PendingQueryMarks(n.Child, inputs, dst)
	case *plan.RowLockNode:
		dst = PendingQueryMarks(n.Child, inputs, dst)
		return AppendPendingRowMarks(dst, n.Marks, inputs)
	case *plan.SourceNode:
		return pendingSourceMarks(QuerySourceAt(inputs, n.Occurrence), dst)
	case *plan.JoinNode:
		if n.Kind != parser.JoinInner {
			// Outer joins consume each local input mark before ON matching.
			// Do not lift it back onto matched or NULL-extended output.
			return dst
		}
		dst = PendingQueryMarks(n.Left, inputs, dst)
		return PendingQueryMarks(n.Right, inputs, dst)
	case *plan.SortNode, *plan.AggregateNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.SetNode, *plan.LimitNode:
		// These owners consume descendant pending marks. Own marks sit above
		// their Sort and therefore are discovered separately by plan.RowLockNode.
		return dst
	default:
		return dst
	}
}
func pendingSourceMarks(source *Source, dst []queryPendingMark) []queryPendingMark {
	if source.Input != nil {
		return pendingSourceMarks(source.Input, dst)
	}
	if source.NestedRoot != nil {
		return PendingQueryMarks(source.NestedRoot, source.NestedInputs, dst)
	}
	return dst
}

// Stored producers own foreign tuple consumption, while local physical marks
// retain their enclosing qualification frontier. Borrow immutable occurrence
// slices from the existing manifest; only the selected manifest is new.
func pendingForeignQueryMarks(pending []queryPendingMark, locks *LockContext) []queryPendingMark {
	var foreign []queryPendingMark
	for _, mark := range pending {
		for i, occurrence := range mark.Occurrences {
			if locks.Leaves[occurrence].Source.Foreign != nil {
				foreign = append(foreign, queryPendingMark{Occurrences: mark.Occurrences[i : i+1], Mark: mark.Mark})
			}
		}
	}
	return foreign
}
func SettlePendingQueryMarks(ctx context.Context, state *State, node plan.Node, child Stage) (Stage, error) {
	if len(child.Pending) == 0 {
		return child, nil
	}
	return ArbitrateViewQuery(ctx, state, node, child)
}
