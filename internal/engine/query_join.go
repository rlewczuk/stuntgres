package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func preflightJoin(c *EvalContext, n *plan.JoinNode) (plan.Node, error) {
	left, err := preflightQuery(c, n.Left)
	if err != nil {
		return nil, err
	}
	right, err := preflightQuery(c, n.Right)
	if err != nil {
		return nil, err
	}
	predicate := n.Predicate
	changed := false
	if predicate != nil {
		predicate, changed, err = preflightCoercions(c, predicate)
		if err != nil {
			return nil, err
		}
	}
	var merges []expr.Expr
	for i, expression := range n.Merges {
		folded, replacement, err := preflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		if replacement {
			if merges == nil {
				merges = append([]expr.Expr(nil), n.Merges...)
			}
			merges[i] = folded
		}
	}
	if changed || merges != nil || left != n.Left || right != n.Right {
		copy := *n
		copy.Left, copy.Right, copy.Predicate = left, right, predicate
		if merges != nil {
			copy.Merges = merges
		}
		return &copy, nil
	}
	return n, nil
}

func openJoinNode(ctx context.Context, state *queryState, n *plan.JoinNode) (indexedStage, error) {
	foreignOwner := n // Private coercion rewrites must not create new scan owners.
	// Admission/preflight is independent of operand population. Opening both
	// children must validate their row-independent coercions even when the
	// other operand yields no candidate; this does not produce either child.
	if n.Predicate != nil {
		evaluation := state.evaluation(ctx)
		predicate, changed, err := preparation.PreflightCoercions(&evaluation, n.Predicate)
		if err != nil {
			return indexedStage{}, err
		}
		if changed {
			copy := *n
			copy.Predicate = predicate
			n = &copy
		}
	}
	if err := initializeJoinEquality(ctx, n.Predicate, n.LeftWidth); err != nil {
		return indexedStage{}, err
	}
	left, err := openQueryNode(ctx, state, n.Left)
	if err != nil {
		return indexedStage{}, err
	}
	right := indexedStage{source: &querySource{flatWidth: n.RightWidth, materialized: true, authority: state.source.authority, host: state.source.host}, schema: n.Right.Schema(), selection: []int{}}
	if n.CorrelatedRight {
		establishDeferredPresence(right.source, n.Right)
	} else {
		right, err = openQueryNode(ctx, state, n.Right)
		if err != nil {
			left.Close()
			return indexedStage{}, err
		}
	}
	width := len(n.Schema())
	if n.LeftWidth < 0 || n.RightWidth < 0 || n.LeftWidth > width || n.RightWidth > width-n.LeftWidth || len(n.Merges) != width-n.LeftWidth-n.RightWidth {
		left.Close()
		right.Close()
		return indexedStage{}, fmt.Errorf("stuntgres: invalid join frame width")
	}
	if n.Kind != parser.JoinInner {
		settledLeft, failure := settlePendingQueryMarks(ctx, state, n.Left, left)
		if failure != nil {
			left.Close()
			right.Close()
			return indexedStage{}, failure
		}
		left = settledLeft
		if !n.CorrelatedRight {
			settledRight, failure := settlePendingQueryMarks(ctx, state, n.Right, right)
			if failure != nil {
				left.Close()
				right.Close()
				return indexedStage{}, failure
			}
			right = settledRight
		}
	}
	if n.CorrelatedRight && (n.Kind == parser.JoinRight || n.Kind == parser.JoinFull || n.CorrelationPrefix < 0) {
		left.Close()
		right.Close()
		return indexedStage{}, fmt.Errorf("stuntgres: invalid correlated join layout")
	}
	if n.CorrelatedRight && n.CorrelationPrefix > 0 && (state.invocation == nil || state.invocation.Scope != 1 || state.invocation.Frame != n.Frame || len(state.invocation.Row) < n.CorrelationPrefix) {
		left.Close()
		right.Close()
		return indexedStage{}, fmt.Errorf("stuntgres: missing inherited function invocation frame")
	}
	var correlationRow []sqltype.Value
	var correlationMoves []presenceCopy
	var correlationPresence []bool
	correlation := state.evaluation(nil)
	if n.CorrelatedRight {
		correlationRow = make([]sqltype.Value, n.CorrelationPrefix+n.LeftWidth)
		correlation.Frame = n.Frame
		correlation.Row = correlationRow
		correlation.presenceSlots = left.source.presenceSlots
		if n.CorrelationPrefix > 0 {
			// Prefix and local-left slots share the bound query frame but
			// may have different packed indices. Compile their union once.
			prefix := querySource{presenceSlots: state.invocation.presenceSlots}
			layout := querySource{}
			correlationMoves, correlationPresence = joinPresence(&layout, &prefix, left.source, 0)
			correlation.presenceSlots = layout.presenceSlots
			correlation.presence = correlationPresence
		}
	}
	invocation := *state
	invocation.foreignOwner = foreignOwner
	if n.CorrelatedRight {
		invocation.invocation = &correlation
	}
	rightIdentity := false
	frame := make([]sqltype.Value, width)
	source := &querySource{flatWidth: width, materialized: true, authority: state.source.authority, host: state.source.host}
	presenceMoves, presence := joinPresence(source, left.source, right.source, n.PresenceSlot)
	locking := state.locking || state.source.locks.rowMarked()
	identity := locking || state.virtualReads
	if identity {
		source.occurrences = appendQueryOccurrences(source.occurrences, left.source)
		source.occurrences = appendQueryOccurrences(source.occurrences, right.source)
	}
	if locking && state.source.locks != nil && !n.CorrelatedRight {
		source.joinLeft, source.joinRight = left.source, right.source
	}
	out := indexedStage{source: source, schema: n.Schema(), selection: []int{}}
	if len(left.pending)+len(right.pending) > 0 {
		out.pending = make([]queryPendingMark, len(left.pending)+len(right.pending))
		copy(out.pending, left.pending)
		copy(out.pending[len(left.pending):], right.pending)
	}
	out.finish = func() error {
		err := left.Close()
		other := right.Close()
		if err != nil {
			return err
		}
		return other
	}
	evaluation := state.evaluation(nil)
	evaluation.Row = frame
	if identity {
		evaluation.queryOccurrences = source.occurrences
		evaluation.queryProvenance = make([]int, 0, len(source.occurrences))
	}
	bindIdentity := func(li, ri int) {
		if identity {
			evaluation.queryProvenance = appendQueryProvenance(evaluation.queryProvenance[:0], left.source, li)
			evaluation.queryProvenance = appendQueryProvenance(evaluation.queryProvenance, right.source, ri)
		}
	}
	evaluation.presenceSlots, evaluation.presence = source.presenceSlots, presence
	leftOccurrence, rightOccurrence := -1, 0
	leftMatched, tail := false, false
	var rightMatched []bool
	preserveLeft := n.Kind == parser.JoinLeft || n.Kind == parser.JoinFull
	preserveRight := n.Kind == parser.JoinRight || n.Kind == parser.JoinFull
	nullFrame := func(start, count int) {
		for i := start; i < start+count; i++ {
			frame[i] = sqltype.ResultNull(n.Schema()[i].Usage)
		}
	}
	emit := func(ctx context.Context, out *indexedStage, li, ri int) (bool, error) {
		evaluation.setContext(ctx)
		bindIdentity(li, ri)
		fillJoinPresence(presence, presenceMoves, left.source.presenceRow(li), right.source.presenceRow(ri))
		for k, expr := range n.Merges {
			value, err := expr.Eval(&evaluation)
			if err != nil {
				return false, err
			}
			if err := validateProducedValue(value, n.Schema()[n.LeftWidth+n.RightWidth+k].Usage); err != nil {
				return false, err
			}
			frame[n.LeftWidth+n.RightWidth+k] = value
		}
		maxInt := int(^uint(0) >> 1)
		if source.flatCount == maxInt || len(source.flat) > maxInt-width || len(source.presenceFlat) > maxInt-len(presence) {
			return false, fmt.Errorf("stuntgres: join result is too large")
		}
		source.flat = append(source.flat, frame...)
		source.presenceFlat = append(source.presenceFlat, presence...)
		if identity {
			source.provenance = append(source.provenance, evaluation.queryProvenance...)
		}
		if locking && state.source.locks != nil {
			source.versions = appendQueryVersions(source.versions, left.source, li, state.source.locks)
			source.versions = appendQueryVersions(source.versions, right.source, ri, state.source.locks)
			if !n.CorrelatedRight {
				source.joinPairs = append(source.joinPairs, queryPair{left: li, right: ri})
			}
		}
		if identity && n.CorrelatedRight {
			source.invocationLineage = append(source.invocationLineage, queryInvocationLineage{left.source, right.source, li, ri})
		}
		out.selection = append(out.selection, source.flatCount)
		source.flatCount++
		return true, ctx.Err()
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		for {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if !tail && leftOccurrence < 0 {
				i, present, err := left.nextOccurrence(ctx)
				if err != nil {
					return false, err
				}
				if !present {
					if !preserveRight {
						return false, nil
					}
					tail, rightOccurrence = true, 0
					nullFrame(0, n.LeftWidth)
				} else {
					leftOccurrence, rightOccurrence, leftMatched = i, 0, false
					row := left.source.row(left.index(i))
					if len(row) != n.LeftWidth {
						return false, fmt.Errorf("stuntgres: invalid left join frame")
					}
					copy(frame, row)
					if n.CorrelatedRight {
						if err := right.Close(); err != nil {
							return false, err
						}
						correlation.setContext(ctx)
						if n.CorrelationPrefix > 0 {
							copy(correlationRow, state.invocation.Row[:n.CorrelationPrefix])
						}
						copy(correlationRow[n.CorrelationPrefix:], row)
						if n.CorrelationPrefix > 0 {
							fillJoinPresence(correlationPresence, correlationMoves, state.invocation.presence, left.source.presenceRow(left.index(i)))
						} else {
							correlation.presence = left.source.presenceRow(left.index(i))
						}
						if identity {
							correlation.queryOccurrences = correlation.queryOccurrences[:0]
							correlation.queryProvenance = correlation.queryProvenance[:0]
							if n.CorrelationPrefix > 0 {
								correlation.queryOccurrences = append(correlation.queryOccurrences, state.invocation.queryOccurrences...)
								correlation.queryProvenance = append(correlation.queryProvenance, state.invocation.queryProvenance...)
							}
							correlation.queryOccurrences = appendQueryOccurrences(correlation.queryOccurrences, left.source)
							correlation.queryProvenance = appendQueryProvenance(correlation.queryProvenance, left.source, left.index(i))
						}
						right, err = openQueryNode(ctx, &invocation, n.Right)
						if err != nil {
							return false, err
						}
						if len(right.pending) != 0 {
							settled, failure := settlePendingQueryMarks(ctx, &invocation, n.Right, right)
							if failure != nil {
								right.Close()
								return false, failure
							}
							right = settled
						}
						if identity && !rightIdentity {
							source.occurrences = appendQueryOccurrences(source.occurrences, right.source)
							evaluation.queryOccurrences = source.occurrences
							rightIdentity = true
						}
					}
				}
			}
			present, err := right.ensure(ctx, rightOccurrence)
			if err != nil {
				return false, err
			}
			if !present {
				if tail {
					return false, nil
				}
				li := left.index(leftOccurrence)
				leftOccurrence = -1
				if preserveLeft && !leftMatched {
					nullFrame(n.LeftWidth, n.RightWidth)
					return emit(ctx, out, li, absentQueryIndex)
				}
				continue
			}
			j := rightOccurrence
			rightOccurrence++
			if preserveRight && j == len(rightMatched) {
				rightMatched = append(rightMatched, false)
			}
			if tail && rightMatched[j] {
				continue
			}
			row := right.source.row(right.index(j))
			if len(row) != n.RightWidth {
				return false, fmt.Errorf("stuntgres: invalid right join frame")
			}
			copy(frame[n.LeftWidth:], row)
			if tail {
				return emit(ctx, out, absentQueryIndex, right.index(j))
			}
			evaluation.setContext(ctx)
			bindIdentity(left.index(leftOccurrence), right.index(j))
			fillJoinPresence(presence, presenceMoves, left.source.presenceRow(left.index(leftOccurrence)), right.source.presenceRow(right.index(j)))
			if n.Predicate != nil {
				matches, err := updateMatches(&evaluation, n.Predicate)
				if err != nil {
					return false, err
				}
				if !matches {
					continue
				}
			}
			leftMatched = true
			if preserveRight {
				rightMatched[j] = true
			}
			return emit(ctx, out, left.index(leftOccurrence), right.index(j))
		}
	}
	return out, nil
}

// Only top-level conjunctive equality keys spanning the two inputs initialize
// a join strategy. Comparisons inside branches remain ordinary scalar demand.
func initializeJoinEquality(ctx context.Context, bound expr.Expr, leftWidth int) error {
	if bound == nil {
		return nil
	}
	if logical, ok := bound.(plan.LogicalExpr); ok && logical.Op == parser.LogicalAnd {
		if err := initializeJoinEquality(ctx, logical.Left, leftWidth); err != nil {
			return err
		}
		return initializeJoinEquality(ctx, logical.Right, leftWidth)
	}
	comparison, ok := bound.(plan.ComparisonExpr)
	if !ok || comparison.Operator != "=" {
		return nil
	}
	side := func(bound expr.Expr) int {
		result := 0
		walkBoundExpression(bound, func(node expr.Expr) {
			column, ok := node.(plan.ColumnExpr)
			if !ok || column.Scope != 1 {
				return
			}
			value := 1
			if column.Slot < leftWidth {
				value = -1
			}
			if result == 0 {
				result = value
			} else if result != value {
				result = 2
			}
		})
		return result
	}
	left, right := side(comparison.Left), side(comparison.Right)
	if left*right != -1 {
		return nil
	}
	return comparison.Predicate.DemandCollation(ctx)
}

// Each dependent invocation owns a different execution-private right source.
// Keep only identity pointers/ordinals, not duplicate row values or regex state.
type queryInvocationLineage struct {
	left, right           *querySource
	leftIndex, rightIndex int
}

func setQueryInvocationLineage(source *querySource, index int) {
	if index == absentQueryIndex {
		if len(source.invocationLineage) != 0 {
			lineage := source.invocationLineage[0]
			setQueryLineage(lineage.left, index)
			setQueryLineage(lineage.right, index)
		}
		return
	}
	lineage := source.invocationLineage[index]
	setQueryLineage(lineage.left, lineage.leftIndex)
	setQueryLineage(lineage.right, lineage.rightIndex)
}
