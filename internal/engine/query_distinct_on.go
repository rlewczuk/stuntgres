package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func openDistinctOnNode(ctx context.Context, state *queryState, n *plan.DistinctOnNode) (out indexedStage, err error) {
	if n.PresenceOnly {
		return openQueryNode(ctx, state, n.Child)
	}
	child, err := openQueryNode(ctx, state, n.Child)
	if err != nil {
		return indexedStage{}, err
	}
	child, err = settlePendingQueryMarks(ctx, state, n.Child, child)
	if err != nil {
		return indexedStage{}, err
	}
	defer func() {
		if err != nil {
			child.Close()
		}
	}()
	width := len(n.Expressions)
	if width == 0 || len(n.Grouping) != width {
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
		evaluation.Context, evaluation.orderValues = ctx, child.orderRow(i)
		evaluation.bindQueryRow(child.source, child.index(i))
		hash := uint64(14695981039346656037)
		for k, e := range n.Expressions {
			value, failure := e.Eval(&evaluation)
			if failure != nil {
				return indexedStage{}, failure
			}
			if failure = validateProducedValue(value, e.ResultUsage()); failure != nil {
				return indexedStage{}, failure
			}
			value, failure = n.Grouping[k].PrepareKey(ctx, value)
			if failure != nil {
				return indexedStage{}, failure
			}
			scratch[k] = value
			h, failure := n.Grouping[k].Hash(ctx, value)
			if failure != nil {
				return indexedStage{}, failure
			}
			hash = (hash ^ h) * 1099511628211
		}
		found, failure := findDistinctTuple(ctx, n.Grouping, scratch, flat, buckets[hash])
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

func preflightDistinctOn(c *EvalContext, n *plan.DistinctOnNode) (plan.Node, error) {
	child, err := preflightQuery(c, n.Child)
	if err != nil {
		return nil, err
	}
	var expressions []expr.Expr
	if !n.PresenceOnly {
		for i, expression := range n.Expressions {
			folded, changed, err := preflightCoercions(c, expression)
			if err != nil {
				return nil, err
			}
			if changed {
				if expressions == nil {
					expressions = append([]expr.Expr(nil), n.Expressions...)
				}
				expressions[i] = folded
			}
		}
	}
	if child == n.Child && expressions == nil {
		return n, nil
	}
	copy := *n
	copy.Child = child
	if expressions != nil {
		copy.Expressions = expressions
	}
	return &copy, nil
}
