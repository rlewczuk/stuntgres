package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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
func producingStage(child *indexedStage, schema []catalog.Column) indexedStage {
	return indexedStage{source: child.source, schema: schema, selection: []int{}, cacheWidth: child.cacheWidth, pending: child.pending, finish: child.Close,
		reset: func(ctx context.Context, out *indexedStage) (bool, error) {
			position := child.position
			changed, err := child.rewind(ctx)
			if err != nil {
				return false, err
			}
			if !changed {
				// Cached parent occurrences still own this child's demand frontier.
				child.position = position
				return false, nil
			}
			out.selection, out.cacheOccurrences, out.orderValues = out.selection[:0], out.cacheOccurrences[:0], out.orderValues[:0]
			out.done = false
			return true, nil
		}}
}

func (b *expressionBinder) scheduleQueryProjection(root LogicalNode, projection []expr.Expr, bounded bool) LogicalNode {
	base := 0
	var lateSlots []int
	ordered, sorting := root.(*sortNode)
	if !sorting && !bounded {
		return root
	}
	if sorting {
		base = ordered.CacheWidth
	}
	var late []expr.Expr
	for i, bound := range projection {
		if querySetDepth(bound) != 0 {
			continue
		}
		if bound.ResultUsage().Type == nil {
			continue
		}
		switch bound.(type) {
		case columnExpression, groupExpression, cachedOrderExpression, unknownOutput, literalExpression, parameterExpression:
			continue
		}
		if sorting && bound.Properties().Volatility != expr.Volatile {
			slot := ordered.CacheWidth
			ordered.Outputs = append(ordered.Outputs, bound)
			ordered.CacheWidth++
			projection[i] = cachedOrderExpression{Info: expr.InfoOf(bound), Slot: slot}
		} else if bounded {
			late = append(late, bound)
			lateSlots = append(lateSlots, i)
		}
	}
	if sorting {
		base = ordered.CacheWidth
	}
	if len(late) == 0 {
		return root
	}
	for j, i := range lateSlots {
		bound := projection[i]
		projection[i] = cachedOrderExpression{Info: expr.InfoOf(bound), Slot: base + j}
	}
	return &evaluationNode{QuerySchema: root.Schema(), Child: root, Expressions: late, Base: base}
}

func openEvaluationNode(ctx context.Context, state *queryState, n *evaluationNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, n.Child)
	if err != nil {
		return indexedStage{}, err
	}
	out := producingStage(&child, n.Schema())
	out.cacheWidth = n.Base + len(n.Expressions)
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
		evaluation.setContext(ctx)
		evaluation.bindQueryRow(child.source, child.index(i))
		evaluation.orderValues = values
		for j, expr := range n.Expressions {
			value, err := expr.Eval(&evaluation)
			if err != nil {
				return false, err
			}
			values[n.Base+j] = value
		}
		out.source = child.source
		out.selection = append(out.selection, child.index(i))
		return true, ctx.Err()
	}
	return out, nil
}

func openLimitNode(ctx context.Context, state *queryState, n *limitNode) (indexedStage, error) {
	if n.PresenceOnly {
		return openQueryNode(ctx, state, n.Child)
	}
	offset, _, err := queryBoundValue(ctx, state, n.Offset, "OFFSET")
	if err != nil {
		return indexedStage{}, err
	}
	count, finite, err := queryBoundValue(ctx, state, n.Count, "LIMIT")
	if err != nil {
		return indexedStage{}, err
	}
	if finite && count == 0 {
		return indexedStage{source: state.source, schema: n.Schema(), selection: []int{}}, nil
	}
	child, err := openQueryNode(ctx, state, n.Child)
	if err != nil {
		return indexedStage{}, err
	}
	// A bounded view cannot defer inherited rowmarks past its own survivor
	// count. Plain scalar views still defer until enclosing qualification.
	if state.inherited.Mode != 0 && state.source.locks != nil {
		occurrences := queryTreeOccurrences(n.Child, state.source, nil)
		if len(occurrences) != 0 {
			child.pending = append(child.pending, queryPendingMark{occurrences: occurrences, mark: state.inherited})
		}
	}
	child, err = settlePendingQueryMarks(ctx, state, n.Child, child)
	if err != nil {
		return indexedStage{}, err
	}
	if offset == 0 && !finite {
		return child, nil
	}
	out := producingStage(&child, n.Schema())
	initialOffset, initialCount := offset, count
	baseReset := out.reset
	out.reset = func(ctx context.Context, out *indexedStage) (bool, error) {
		changed, err := baseReset(ctx, out)
		if err != nil || !changed {
			return changed, err
		}
		offset, count = initialOffset, initialCount
		return true, nil
	}
	var boundary []sqltype.Value
	if finite && n.WithTies {
		boundary = make([]sqltype.Value, len(n.PeerKeys))
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		if finite && count == 0 && !n.WithTies {
			if state.retain {
				return false, nil
			}
			return false, child.Close()
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
			if finite && count == 0 {
				peer, err := queryTiesPeer(ctx, &child, i, n.PeerKeys, boundary, state.input)
				if err != nil {
					return false, err
				}
				if !peer {
					if state.retain {
						return false, nil
					}
					return false, child.Close()
				}
			}
			retainOccurrence(out, &child, i)
			if finite && count > 0 {
				count--
				if count == 0 && n.WithTies {
					captureQueryTiesBoundary(&child, i, n.PeerKeys, boundary)
				}
			}
			return true, ctx.Err()
		}
	}
	return out, nil
}

func validateProducedValue(value sqltype.Value, usage sqltype.Usage) error {
	if !value.ValidResult(usage) {
		return fmt.Errorf("stuntgres: invalid produced query value")
	}
	return nil
}
