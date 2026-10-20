package engine

import "stuntgres/internal/engine/plan"

// Absence is carried by occurrence identity, never inferred from NULL cells or
// a zero-width physical row. Missing subtrees must not evaluate their recipes.
func absentLockedSource(source *querySource, locks *queryLockContext) bool {
	if source.input != nil {
		return absentLockedSource(source.input, locks)
	}
	if source.lineage != nil {
		return source.lineage.currentIndex == absentQueryIndex
	}
	if source.singleton || source.materialized {
		return false
	}
	return locks.leaves[source.occurrence].absent
}
func absentLockedQuery(node plan.Node, inputs *querySource, locks *queryLockContext) bool {
	switch n := node.(type) {
	case *plan.DistinctNode, *plan.AggregateNode, *plan.SetNode, *plan.RecursiveUnionNode, *plan.WorktableScanNode:
		return false // Materialized tuples no longer carry physical absence.
	case *plan.DerivedNode:
		if n.CTE != nil {
			return false
		}
		return absentLockedQuery(n.Query.Root, inputs, locks)
	case *plan.SourceNode:
		return absentLockedSource(querySourceAt(inputs, n.Occurrence), locks)
	case *plan.JoinNode:
		return absentLockedQuery(n.Left, inputs, locks) && absentLockedQuery(n.Right, inputs, locks)
	case *plan.ProjectNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *plan.FilterNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *plan.EvaluationNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *plan.SortNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *plan.DistinctOnNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *plan.LimitNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *plan.RowLockNode:
		return absentLockedQuery(n.Child, inputs, locks)
	}
	return false
}
