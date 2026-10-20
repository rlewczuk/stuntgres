package analyze

import (
	"stuntgres/internal/engine/plan"
)

// The plan node kinds a frozen view namespace names (ViewNamespace.Node).
const (
	ViewSourceNamespace uint8 = iota + 1
	ViewJoinNamespace
	ViewValuesNamespace
	ViewDerivedNamespace
	ViewFunctionNamespace
	ViewRegexpNamespace
)

// FrozenNamespaceNode returns the node kind and ordinal that identify the
// FROM item under root in a frozen view namespace.
func FrozenNamespaceNode(root plan.Node) (uint8, int) {
	switch node := ViewFromRoot(root).(type) {
	case *plan.FunctionFromNode:
		if node.RegexpNamespace {
			return ViewRegexpNamespace, node.ViewOrdinal
		}
		return ViewFunctionNamespace, node.ViewOrdinal
	case *plan.SourceNode:
		return ViewSourceNamespace, node.Occurrence
	case *plan.JoinNode:
		return ViewJoinNamespace, node.ViewOrdinal
	case *plan.ValuesNode:
		return ViewValuesNamespace, node.ViewOrdinal
	case *plan.DerivedNode:
		if node.CTE != nil {
			return ViewDerivedNamespace, node.Ordinal
		}
		return ViewDerivedNamespace, int(node.Query.Root.(*plan.ProjectNode).Frame)
	case *plan.WorktableScanNode:
		return ViewDerivedNamespace, node.Ordinal
	}
	return 0, 0
}

// ViewFromRoot returns a query level's FROM item, skipping the nodes the
// level stacks above it (projections, filters, grouping, windows, ordering,
// DISTINCT, LIMIT and row locks).
func ViewFromRoot(node plan.Node) plan.Node {
	for {
		switch n := node.(type) {
		case *plan.SetProjectNode:
			node = n.Child
		case *plan.LimitNode:
			node = n.Child
		case *plan.EvaluationNode:
			node = n.Child
		case *plan.RowLockNode:
			node = n.Child
		case *plan.ProjectNode:
			node = n.Child
		case *plan.FilterNode:
			node = n.Child
		case *plan.SortNode:
			node = n.Child
		case *plan.AggregateNode:
			node = n.Child
		case *plan.WindowNode:
			node = n.Child
		case *plan.DistinctNode:
			node = n.Child
		case *plan.DistinctOnNode:
			node = n.Child
		default:
			return node
		}
	}
}
