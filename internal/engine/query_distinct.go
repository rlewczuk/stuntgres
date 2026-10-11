package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype"
)

// distinctNode owns source-typed projected tuples. Its parent projection reads
// slots, so ordering and INSERT assignment never reevaluate key expressions.
type distinctNode struct {
	querySchema
	child        LogicalNode
	expressions  []BoundExpr
	grouping     []sqltype.Grouping
	presenceOnly bool // Retain analyzed targets while discarding eligible EXISTS demand.
}

func openDistinctNode(ctx context.Context, state *queryState, n *distinctNode) (out indexedStage, err error) {
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
		if closeErr := child.Close(); err == nil && closeErr != nil {
			out, err = indexedStage{}, closeErr
		}
	}()
	width := len(n.expressions)
	if width == 0 || len(n.grouping) != width || len(n.Schema()) != width {
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
		for k, expression := range n.expressions {
			value, failure := expression.Eval(&evaluation)
			if failure != nil {
				return indexedStage{}, failure
			}
			if failure := validateProducedValue(value, n.Schema()[k].Usage); failure != nil {
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
		index, failure := findDistinctTuple(ctx, n.grouping, scratch, source.flat, buckets[hash])
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
func findDistinctTuple(ctx context.Context, grouping []sqltype.Grouping, tuple, flat []sqltype.Value, candidates []int) (int, error) {
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

func preflightDistinct(c *EvalContext, n *distinctNode) (LogicalNode, error) {
	if n.presenceOnly {
		child, err := preflightQuery(c, n.child)
		if err != nil {
			return nil, err
		}
		if child == n.child {
			return n, nil
		}
		copy := *n
		copy.child = child
		return &copy, nil
	}
	child, err := preflightQuery(c, n.child)
	if err != nil {
		return nil, err
	}
	var expressions []BoundExpr
	for i, expression := range n.expressions {
		folded, changed, err := preflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		if owned, did := queryScalarOwner(child, folded); did {
			folded, changed = owned, true
		}
		if changed {
			if expressions == nil {
				expressions = append([]BoundExpr(nil), n.expressions...)
			}
			expressions[i] = folded
		}
	}
	if child != n.child || expressions != nil {
		copy := *n
		copy.child = child
		if expressions != nil {
			copy.expressions = expressions
		}
		return &copy, nil
	}
	return n, nil
}
