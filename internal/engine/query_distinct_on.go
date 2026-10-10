package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// Unlike DISTINCT, ON retains input occurrences and never demands payload
// equality. The existing cache and source own retained rows and key datums.
type distinctOnNode struct {
	querySchema
	child        LogicalNode
	expressions  []BoundExpr
	grouping     []sqltype.Grouping
	presenceOnly bool
}

func openDistinctOnNode(ctx context.Context, state *queryState, n *distinctOnNode) (out indexedStage, err error) {
	if n.presenceOnly {
		return openQueryNode(ctx, state, n.child)
	}
	child, err := openQueryNode(ctx, state, n.child)
	if err != nil {
		return indexedStage{}, err
	}
	child, err = settlePendingQueryMarks(ctx, state, n.child, child)
	if err != nil {
		return indexedStage{}, err
	}
	defer func() {
		if err != nil {
			child.Close()
		}
	}()
	width := len(n.expressions)
	if width == 0 || len(n.grouping) != width {
		return indexedStage{}, fmt.Errorf("stuntgres: invalid DISTINCT ON keys")
	}
	out = producingStage(&child, n.Schema())
	scratch := make([]sqltype.Value, width)
	var flat []sqltype.Value
	buckets := make(map[uint64][]int)
	evaluation := state.evaluation(nil)
	for {
		i, present, failure := child.nextOccurrence(ctx)
		if failure != nil {
			return indexedStage{}, failure
		}
		if !present {
			break
		}
		evaluation.Context, evaluation.Row, evaluation.orderValues = ctx, child.source.row(child.index(i)), child.orderRow(i)
		hash := uint64(14695981039346656037)
		for k, e := range n.expressions {
			value, failure := e.Eval(&evaluation)
			if failure != nil {
				return indexedStage{}, failure
			}
			if failure = validateProducedValue(value, e.ResultUsage()); failure != nil {
				return indexedStage{}, failure
			}
			value, failure = n.grouping[k].PrepareKey(ctx, value)
			if failure != nil {
				return indexedStage{}, failure
			}
			scratch[k] = value
			h, failure := n.grouping[k].Hash(ctx, value)
			if failure != nil {
				return indexedStage{}, failure
			}
			hash = (hash ^ h) * 1099511628211
		}
		found, failure := findDistinctTuple(ctx, n.grouping, scratch, flat, buckets[hash])
		if failure != nil {
			return indexedStage{}, failure
		}
		if found < 0 {
			buckets[hash] = append(buckets[hash], len(flat)/width)
			flat = append(flat, scratch...)
			retainOccurrence(&out, &child, i)
		}
	}
	if err = ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	return out, nil
}

func preflightDistinctOn(c *EvalContext, n *distinctOnNode) (LogicalNode, error) {
	child, err := preflightQuery(c, n.child)
	if err != nil {
		return nil, err
	}
	var expressions []BoundExpr
	if !n.presenceOnly {
		for i, expression := range n.expressions {
			folded, changed, err := preflightCoercions(c, expression)
			if err != nil {
				return nil, err
			}
			if changed {
				if expressions == nil {
					expressions = append([]BoundExpr(nil), n.expressions...)
				}
				expressions[i] = folded
			}
		}
	}
	if child == n.child && expressions == nil {
		return n, nil
	}
	copy := *n
	copy.child = child
	if expressions != nil {
		copy.expressions = expressions
	}
	return &copy, nil
}
