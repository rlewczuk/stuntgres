package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// SupportsBackwardCursor classifies omitted-SCROLL capability of the admitted
// native producer, not Stage.Rewind and not PostgreSQL cost-selected plan parity.
// Explicit SCROLL is independent; its caller also excludes row-marked queries.
// Inspection never opens/evaluates a producer or mutates captured sources.
func SupportsBackwardCursor(root plan.Node, inputs *Source) bool {
	switch n := root.(type) {
	case *plan.SourceNode:
		return backwardCursorSource(QuerySourceAt(inputs, n.Occurrence))
	case *plan.SingletonNode:
		return false
	case *plan.ValuesNode:
		// A sole nonvolatile VALUES row is a childless Result, not a
		// surviving ValuesScan. The expressions remain unevaluated here.
		if len(n.Rows) == 1 {
			for _, value := range n.Rows[0] {
				if value.Properties().Volatility == expr.Volatile {
					return true
				}
			}
			return false
		}
		return true
	case *plan.ProjectNode:
		return SupportsBackwardCursor(n.Child, inputs)
	case *plan.FilterNode:
		return SupportsBackwardCursor(n.Child, inputs)
	case *plan.EvaluationNode:
		return SupportsBackwardCursor(n.Child, inputs)
	case *plan.LimitNode:
		return SupportsBackwardCursor(n.Child, inputs)
	case *plan.SortNode:
		return !n.PresenceOnly || SupportsBackwardCursor(n.Child, inputs)
	case *plan.RowLockNode:
		return SupportsBackwardCursor(n.Child, inputs)
	case *plan.FunctionFromNode:
		for _, call := range n.Calls {
			if _, constant := call.(plan.LiteralExpr); !constant {
				return true
			}
		}
		return false
	case *plan.AggregateNode:
		return n.PresenceOnly && SupportsBackwardCursor(n.Child, inputs)
	case *plan.DistinctNode:
		if !n.PresenceOnly {
			if len(n.Expressions) == 0 {
				return false
			}
			for _, value := range n.Expressions {
				if _, literal := value.(plan.LiteralExpr); !literal {
					return false
				}
			}
		}
		// All physical literal keys are redundant: DISTINCT is a LIMIT 1
		// over its child. Do not extend this to arbitrary row-free calls.
		return SupportsBackwardCursor(n.Child, inputs)
	case *plan.DistinctOnNode:
		return n.PresenceOnly && SupportsBackwardCursor(n.Child, inputs)
	case *plan.DerivedNode:
		if n.CTE != nil && !n.CTE.Folded() {
			return true
		}
		return n.Query != nil && SupportsBackwardCursor(n.Query.Root, inputs)
	case *plan.SetNode:
		return n.Operation == "union" && n.All && SupportsBackwardCursor(n.Left, inputs) && SupportsBackwardCursor(n.Right, inputs)
	default:
		// Joins, active aggregate/window/set stages, target SRFs and opaque
		// recursive/worktable edges do not promise backward scan capability.
		return false
	}
}

func backwardCursorSource(source *Source) bool {
	for {
		if source.Foreign != nil {
			return false
		}
		if source.Input == nil {
			break
		}
		source = source.Input
	}
	if source.NestedRoot != nil {
		return SupportsBackwardCursor(source.NestedRoot, source.NestedInputs)
	}
	return !source.Singleton
}
