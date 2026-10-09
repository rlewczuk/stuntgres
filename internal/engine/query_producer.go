package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// Producers append only demanded occurrence identities. Blocking stages drain
// this same seam; the public boundary still publishes only finalized results.
func (s *indexedStage) ensure(ctx context.Context, i int) (bool, error) {
	if s.terminal != nil {
		return false, s.terminal
	}
	if s.closed {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, s.fail(err)
	}
	for s.produce != nil && !s.done && i >= len(s.selection) {
		if err := ctx.Err(); err != nil {
			return false, s.fail(err)
		}
		present, err := s.produce(ctx, s)
		if err != nil {
			return false, s.fail(err)
		}
		if !present {
			s.done = true
		}
	}
	if err := ctx.Err(); err != nil {
		return false, s.fail(err)
	}
	return i < s.count(), nil
}
func (s *indexedStage) nextOccurrence(ctx context.Context) (int, bool, error) {
	present, err := s.ensure(ctx, s.position)
	if err != nil || !present {
		return 0, present, err
	}
	i := s.position
	s.position++
	return i, true, nil
}
func (s *indexedStage) drain(ctx context.Context) error {
	if s.terminal != nil {
		return s.terminal
	}
	if s.closed {
		return nil
	}
	if s.produce == nil {
		if err := ctx.Err(); err != nil {
			return s.fail(err)
		}
		return nil
	}
	for !s.done {
		if _, err := s.ensure(ctx, len(s.selection)); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	return nil
}
func retainOccurrence(out *indexedStage, child *indexedStage, i int) {
	out.source = child.source
	out.selection = append(out.selection, child.index(i))
	out.cacheWidth, out.orderValues = child.cacheWidth, child.orderValues
	if child.cacheWidth != 0 {
		out.cacheOccurrences = append(out.cacheOccurrences, child.orderOccurrence(i))
	}
}
func producingStage(child *indexedStage, schema []ColumnDescriptor) indexedStage {
	return indexedStage{source: child.source, schema: schema, selection: []int{}, cacheWidth: child.cacheWidth, pending: child.pending, finish: child.Close}
}

// Evaluation keeps the input frame/provenance intact and shares the existing
// ORDER cache authority. Final projection reads these slots, never reevaluates.
type evaluationNode struct {
	querySchema
	child       LogicalNode
	expressions []BoundExpr
	base        int
}

func (b *expressionBinder) scheduleQueryProjection(root LogicalNode, projection []BoundExpr, bounded bool) LogicalNode {
	base := 0
	var lateSlots []int
	ordered, sorting := root.(*sortNode)
	if !sorting && !bounded {
		return root
	}
	if sorting {
		base = ordered.cacheWidth
	}
	var late []BoundExpr
	for i, expr := range projection {
		if expr.ResultUsage().Type == nil {
			continue
		}
		switch expr.(type) {
		case columnExpression, groupExpression, cachedOrderExpression, unknownOutput, literalExpression, parameterExpression:
			continue
		}
		if sorting && expr.Properties().Volatility != Volatile {
			slot := ordered.cacheWidth
			ordered.outputs = append(ordered.outputs, expr)
			ordered.cacheWidth++
			projection[i] = cachedOrderExpression{expressionInfo: boundExpressionInfo(expr), slot: slot}
		} else if bounded {
			late = append(late, expr)
			lateSlots = append(lateSlots, i)
		}
	}
	if sorting {
		base = ordered.cacheWidth
	}
	if len(late) == 0 {
		return root
	}
	for j, i := range lateSlots {
		expr := projection[i]
		projection[i] = cachedOrderExpression{expressionInfo: boundExpressionInfo(expr), slot: base + j}
	}
	return &evaluationNode{querySchema: root.Schema(), child: root, expressions: late, base: base}
}

func openEvaluationNode(ctx context.Context, state *queryState, n *evaluationNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, n.child)
	if err != nil {
		return indexedStage{}, err
	}
	out := producingStage(&child, n.Schema())
	out.cacheWidth = n.base + len(n.expressions)
	evaluation := state.evaluation(nil)
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		i, present, err := child.nextOccurrence(ctx)
		if err != nil || !present {
			return present, err
		}
		start := len(out.orderValues)
		out.orderValues = append(out.orderValues, make([]sqltype.Value, out.cacheWidth)...)
		values := out.orderValues[start:]
		copy(values, child.orderRow(i))
		evaluation.Context, evaluation.Row, evaluation.orderValues = ctx, child.source.row(child.index(i)), values
		for j, expr := range n.expressions {
			value, err := expr.Eval(&evaluation)
			if err != nil {
				return false, err
			}
			values[n.base+j] = value
		}
		out.source = child.source
		out.selection = append(out.selection, child.index(i))
		return true, ctx.Err()
	}
	return out, nil
}

func openLimitNode(ctx context.Context, state *queryState, n *limitNode) (indexedStage, error) {
	offset, _, err := queryBoundValue(ctx, state, n.offset, "OFFSET")
	if err != nil {
		return indexedStage{}, err
	}
	count, finite, err := queryBoundValue(ctx, state, n.count, "LIMIT")
	if err != nil {
		return indexedStage{}, err
	}
	if finite && count == 0 {
		return indexedStage{source: state.source, schema: n.Schema(), selection: []int{}}, nil
	}
	child, err := openQueryNode(ctx, state, n.child)
	if err != nil {
		return indexedStage{}, err
	}
	// A bounded view cannot defer inherited rowmarks past its own survivor
	// count. Plain scalar views still defer until enclosing qualification.
	if state.inherited.mode != 0 && state.source.locks != nil {
		occurrences := queryTreeOccurrences(n.child, state.source, nil)
		if len(occurrences) != 0 {
			child.pending = append(child.pending, queryPendingMark{occurrences: occurrences, mark: state.inherited})
		}
	}
	child, err = settlePendingQueryMarks(ctx, state, n.child, child)
	if err != nil {
		return indexedStage{}, err
	}
	if offset == 0 && !finite {
		return child, nil
	}
	out := producingStage(&child, n.Schema())
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		if finite && count == 0 {
			return false, nil
		}
		for {
			i, present, err := child.nextOccurrence(ctx)
			if err != nil || !present {
				return present, err
			}
			if offset > 0 {
				offset--
				continue
			}
			retainOccurrence(out, &child, i)
			if finite {
				count--
			}
			return true, ctx.Err()
		}
	}
	return out, nil
}

func validateProducedValue(value sqltype.Value, usage sqltype.Usage) error {
	if value.Type() == nil || !value.Type().Concrete() || value.Type() != usage.Type {
		return fmt.Errorf("stuntgres: invalid produced query value")
	}
	return nil
}
