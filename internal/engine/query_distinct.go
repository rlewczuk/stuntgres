package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func openDistinctNode(ctx context.Context, state *queryState, n *plan.DistinctNode) (out indexedStage, err error) {
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
		if closeErr := child.Close(); err == nil && closeErr != nil {
			out, err = indexedStage{}, closeErr
		}
	}()
	width := len(n.Expressions)
	if width == 0 || len(n.Grouping) != width || len(n.Schema()) != width {
		return indexedStage{}, fmt.Errorf("stuntgres: invalid DISTINCT schema")
	}
	source := &querySource{flatWidth: width, materialized: true, authority: state.source.authority, scope: state.source.scope}
	scratch := make([]sqltype.Value, width)
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
		evaluation.Context = ctx
		evaluation.bindQueryRow(child.source, child.index(i))
		if child.cacheWidth != 0 {
			evaluation.orderValues = child.orderRow(i)
		}
		hash := uint64(14695981039346656037)
		for k, expression := range n.Expressions {
			value, failure := expression.Eval(&evaluation)
			if failure != nil {
				return indexedStage{}, failure
			}
			if failure := validateProducedValue(value, n.Schema()[k].Usage); failure != nil {
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
		index, failure := findDistinctTuple(ctx, n.Grouping, scratch, source.flat, buckets[hash])
		if failure != nil {
			return indexedStage{}, failure
		}
		if index < 0 {
			buckets[hash] = append(buckets[hash], source.flatCount)
			source.flat = append(source.flat, scratch...)
			source.flatCount++
		}
	}
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	return indexedStage{source: source, schema: n.Schema()}, nil
}

// Hash collisions are candidate lists, never evidence of tuple equality.
func findDistinctTuple[G interface {
	Equal(context.Context, sqltype.Value, sqltype.Value) (bool, error)
}](ctx context.Context, grouping []G, tuple, flat []sqltype.Value, candidates []int) (int, error) {
	for _, index := range candidates {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		row := flat[index*len(grouping) : (index+1)*len(grouping)]
		equal := true
		for i, family := range grouping {
			match, err := family.Equal(ctx, tuple[i], row[i])
			if err != nil {
				return -1, err
			}
			if !match {
				equal = false
				break
			}
		}
		if equal {
			return index, ctx.Err()
		}
	}
	return -1, ctx.Err()
}

func preflightDistinct(c *EvalContext, n *plan.DistinctNode) (plan.Node, error) {
	if n.PresenceOnly {
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return n, nil
		}
		copy := *n
		copy.Child = child
		return &copy, nil
	}
	child, err := preflightQuery(c, n.Child)
	if err != nil {
		return nil, err
	}
	var expressions []expr.Expr
	for i, expression := range n.Expressions {
		folded, changed, err := preflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		if owned, did := queryScalarOwner(child, folded); did {
			folded, changed = owned, true
		}
		if changed {
			if expressions == nil {
				expressions = append([]expr.Expr(nil), n.Expressions...)
			}
			expressions[i] = folded
		}
	}
	if child != n.Child || expressions != nil {
		copy := *n
		copy.Child = child
		if expressions != nil {
			copy.Expressions = expressions
		}
		return &copy, nil
	}
	return n, nil
}
