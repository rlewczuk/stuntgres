package plan

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// ExplainEstimate describes logical work before execution-time folding. Costs
// are inclusive logical units, never disk costs or execution measurements.
type ExplainEstimate struct {
	Rows, Cost float64
}

const ExplainEstimateLimit = 1e12

func explainFinite(n float64) float64 {
	if math.IsNaN(n) || n < 0 {
		return 0
	}
	return min(n, ExplainEstimateLimit)
}

func (e ExplainEstimate) RoundedRows() float64 { return math.Ceil(explainFinite(e.Rows)) }

func explainLiteralCount(expression expr.Expr) (float64, bool) {
	literal, ok := expression.(LiteralExpr)
	if !ok || literal.Value.IsNull() {
		return 0, false
	}
	value, err := literal.Value.Integer()
	return float64(max(value, 0)), err == nil
}

// ExplainInactive follows the executor's presence-only demand. Analysis-only
// definitions remain bound but must not be displayed as active work.
func ExplainInactive(node Node) bool {
	switch n := node.(type) {
	case *SortNode:
		return n.PresenceOnly
	case *DistinctNode:
		return n.PresenceOnly
	case *DistinctOnNode:
		return n.PresenceOnly
	case *LimitNode:
		return n.PresenceOnly
	case *AggregateNode:
		return n.PresenceOnly && n.DiscardableGrouping()
	case *WindowNode:
		for _, call := range n.Calls {
			if !call.AnalysisOnly {
				return false
			}
		}
		return true
	}
	return false
}

// EstimateExplainNode consumes detached child/source estimates only. In
// particular it never counts rows, calls a provider, or evaluates expressions.
func EstimateExplainNode(node Node, children []ExplainEstimate, sourceRows float64) (ExplainEstimate, error) {
	var left, right ExplainEstimate
	if len(children) > 0 {
		left = children[0]
	}
	if len(children) > 1 {
		right = children[1]
	}
	if ExplainInactive(node) {
		return left, nil
	}
	out := ExplainEstimate{Rows: left.Rows, Cost: left.Cost + right.Cost}
	local := left.Rows * .01
	switch n := node.(type) {
	case *SourceNode, *WorktableScanNode, *FunctionFromNode:
		out.Rows, local = explainFinite(sourceRows), explainFinite(sourceRows)
	case *SingletonNode:
		out.Rows, local = 1, 1
	case *ValuesNode:
		out.Rows = float64(len(n.Rows))
		local = out.Rows
	case *FilterNode:
		out.Rows *= defaultFilterFraction(n.Predicate)
		if literal, ok := n.Predicate.(LiteralExpr); ok {
			truth, err := TruthOf(literal.Value)
			if err == nil {
				if truth == True {
					out.Rows = left.Rows
				} else {
					out.Rows = 0
				}
			}
		}
	case *SortNode:
		local = left.Rows * math.Log2(max(2, left.Rows)) * .01
	case *ProjectNode, *EvaluationNode, *RowLockNode, *WindowNode, *DerivedNode:
	case *DistinctNode, *DistinctOnNode:
		out.Rows /= 2
	case *AggregateNode:
		group := func(keys int) float64 {
			if keys == 0 {
				return 1
			}
			return min(left.Rows, math.Ceil(math.Sqrt(left.Rows)))
		}
		out.Rows = group(len(n.Keys))
		if n.Sets != nil {
			out.Rows = 0
			for _, set := range n.Sets {
				out.Rows += group(len(set))
			}
		}
	case *JoinNode:
		out.Rows = left.Rows * right.Rows
		local = out.Rows * .01
		if n.Predicate != nil {
			out.Rows /= 2
		}
		switch n.Kind {
		case parser.JoinLeft:
			out.Rows = max(out.Rows, left.Rows)
		case parser.JoinRight:
			out.Rows = max(out.Rows, right.Rows)
		case parser.JoinFull:
			out.Rows = max(out.Rows, left.Rows, right.Rows)
		}
	case *RecursiveUnionNode:
		out.Rows = left.Rows + 10*right.Rows
		local = 10 * right.Rows * .01
	case *SetNode:
		local = (left.Rows + right.Rows) * .01
		switch n.Operation {
		case "UNION", "union":
			out.Rows = left.Rows + right.Rows
			if !n.All {
				out.Rows /= 2
			}
		case "INTERSECT", "intersect":
			out.Rows = min(left.Rows, right.Rows) / 2
		case "EXCEPT", "except":
			out.Rows = left.Rows / 2
		default:
			return ExplainEstimate{}, fmt.Errorf("stuntgres: invalid logical set operation %q", n.Operation)
		}
	case *SetProjectNode:
		out.Rows *= 1000
	case *LimitNode:
		if offset, ok := explainLiteralCount(n.Offset); ok {
			out.Rows = max(0, out.Rows-offset)
		}
		if !n.WithTies {
			if count, ok := explainLiteralCount(n.Count); ok {
				out.Rows = min(out.Rows, count)
			}
		}
	default:
		return ExplainEstimate{}, fmt.Errorf("stuntgres: invalid logical EXPLAIN node")
	}
	out.Rows, out.Cost = explainFinite(out.Rows), explainFinite(out.Cost+local)
	return out, nil
}

// ExplainMutationEstimate distinguishes candidate work from emitted RETURNING
// rows. The source already includes its actual predicate's heuristic.
func ExplainMutationEstimate(source ExplainEstimate, returning bool) ExplainEstimate {
	rows := float64(0)
	if returning {
		rows = source.Rows
	}
	return ExplainEstimate{Rows: explainFinite(rows), Cost: explainFinite(source.Cost + source.Rows*.02)}
}
