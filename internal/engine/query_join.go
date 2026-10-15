package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type joinNode struct {
	viewOrdinal  int
	presenceSlot int
	querySchema
	kind                  parser.JoinKind
	left, right           LogicalNode
	predicate             BoundExpr
	leftWidth, rightWidth int
	merges                []BoundExpr
	correlatedRight       bool
	frame                 uint32
	correlationPrefix     int
	captures              []functionCorrelation
}

func preflightJoin(c *EvalContext, n *joinNode) (LogicalNode, error) {
	left, err := preflightQuery(c, n.left)
	if err != nil {
		return nil, err
	}
	right, err := preflightQuery(c, n.right)
	if err != nil {
		return nil, err
	}
	predicate := n.predicate
	changed := false
	if predicate != nil {
		predicate, changed, err = preflightCoercions(c, predicate)
		if err != nil {
			return nil, err
		}
	}
	var merges []BoundExpr
	for i, expression := range n.merges {
		folded, replacement, err := preflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		if replacement {
			if merges == nil {
				merges = append([]BoundExpr(nil), n.merges...)
			}
			merges[i] = folded
		}
	}
	if changed || merges != nil || left != n.left || right != n.right {
		copy := *n
		copy.left, copy.right, copy.predicate = left, right, predicate
		if merges != nil {
			copy.merges = merges
		}
		return &copy, nil
	}
	return n, nil
}

func openJoinNode(ctx context.Context, state *queryState, n *joinNode) (indexedStage, error) {
	foreignOwner := n // Private coercion rewrites must not create new scan owners.
	// Admission/preflight is independent of operand population. Opening both
	// children must validate their row-independent coercions even when the
	// other operand yields no candidate; this does not produce either child.
	if n.predicate != nil {
		evaluation := state.evaluation(ctx)
		predicate, changed, err := preflightCoercions(&evaluation, n.predicate)
		if err != nil {
			return indexedStage{}, err
		}
		if changed {
			copy := *n
			copy.predicate = predicate
			n = &copy
		}
	}
	if err := initializeJoinEquality(ctx, n.predicate, n.leftWidth); err != nil {
		return indexedStage{}, err
	}
	left, err := openQueryNode(ctx, state, n.left)
	if err != nil {
		return indexedStage{}, err
	}
	right := indexedStage{source: &querySource{flatWidth: n.rightWidth, materialized: true, authority: state.source.authority, scope: state.source.scope}, schema: n.right.Schema(), selection: []int{}}
	if n.correlatedRight {
		establishDeferredPresence(right.source, n.right)
	} else {
		right, err = openQueryNode(ctx, state, n.right)
		if err != nil {
			left.Close()
			return indexedStage{}, err
		}
	}
	width := len(n.Schema())
	if n.leftWidth < 0 || n.rightWidth < 0 || n.leftWidth > width || n.rightWidth > width-n.leftWidth || len(n.merges) != width-n.leftWidth-n.rightWidth {
		left.Close()
		right.Close()
		return indexedStage{}, fmt.Errorf("stuntgres: invalid join frame width")
	}
	if n.kind != parser.JoinInner {
		settledLeft, failure := settlePendingQueryMarks(ctx, state, n.left, left)
		if failure != nil {
			left.Close()
			right.Close()
			return indexedStage{}, failure
		}
		left = settledLeft
		if !n.correlatedRight {
			settledRight, failure := settlePendingQueryMarks(ctx, state, n.right, right)
			if failure != nil {
				left.Close()
				right.Close()
				return indexedStage{}, failure
			}
			right = settledRight
		}
	}
	if n.correlatedRight && (n.kind == parser.JoinRight || n.kind == parser.JoinFull || n.correlationPrefix < 0) {
		left.Close()
		right.Close()
		return indexedStage{}, fmt.Errorf("stuntgres: invalid correlated join layout")
	}
	if n.correlatedRight && n.correlationPrefix > 0 && (state.invocation == nil || state.invocation.Scope != 1 || state.invocation.Frame != n.frame || len(state.invocation.Row) < n.correlationPrefix) {
		left.Close()
		right.Close()
		return indexedStage{}, fmt.Errorf("stuntgres: missing inherited function invocation frame")
	}
	var correlationRow []sqltype.Value
	var correlationMoves []presenceCopy
	var correlationPresence []bool
	correlation := state.evaluation(nil)
	if n.correlatedRight {
		correlationRow = make([]sqltype.Value, n.correlationPrefix+n.leftWidth)
		correlation.Frame = n.frame
		correlation.Row = correlationRow
		correlation.presenceSlots = left.source.presenceSlots
		if n.correlationPrefix > 0 {
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
	if n.correlatedRight {
		invocation.invocation = &correlation
	}
	rightIdentity := false
	frame := make([]sqltype.Value, width)
	source := &querySource{flatWidth: width, materialized: true, authority: state.source.authority, scope: state.source.scope}
	presenceMoves, presence := joinPresence(source, left.source, right.source, n.presenceSlot)
	locking := state.locking || state.source.locks.rowMarked()
	identity := locking || state.virtualReads
	if identity {
		source.occurrences = appendQueryOccurrences(source.occurrences, left.source)
		source.occurrences = appendQueryOccurrences(source.occurrences, right.source)
	}
	if locking && state.source.locks != nil && !n.correlatedRight {
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
	preserveLeft := n.kind == parser.JoinLeft || n.kind == parser.JoinFull
	preserveRight := n.kind == parser.JoinRight || n.kind == parser.JoinFull
	nullFrame := func(start, count int) {
		for i := start; i < start+count; i++ {
			frame[i] = sqltype.ResultNull(n.Schema()[i].Usage)
		}
	}
	emit := func(ctx context.Context, out *indexedStage, li, ri int) (bool, error) {
		evaluation.setContext(ctx)
		bindIdentity(li, ri)
		fillJoinPresence(presence, presenceMoves, left.source.presenceRow(li), right.source.presenceRow(ri))
		for k, expr := range n.merges {
			value, err := expr.Eval(&evaluation)
			if err != nil {
				return false, err
			}
			if err := validateProducedValue(value, n.Schema()[n.leftWidth+n.rightWidth+k].Usage); err != nil {
				return false, err
			}
			frame[n.leftWidth+n.rightWidth+k] = value
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
			if !n.correlatedRight {
				source.joinPairs = append(source.joinPairs, queryPair{left: li, right: ri})
			}
		}
		if identity && n.correlatedRight {
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
					nullFrame(0, n.leftWidth)
				} else {
					leftOccurrence, rightOccurrence, leftMatched = i, 0, false
					row := left.source.row(left.index(i))
					if len(row) != n.leftWidth {
						return false, fmt.Errorf("stuntgres: invalid left join frame")
					}
					copy(frame, row)
					if n.correlatedRight {
						if err := right.Close(); err != nil {
							return false, err
						}
						correlation.setContext(ctx)
						if n.correlationPrefix > 0 {
							copy(correlationRow, state.invocation.Row[:n.correlationPrefix])
						}
						copy(correlationRow[n.correlationPrefix:], row)
						if n.correlationPrefix > 0 {
							fillJoinPresence(correlationPresence, correlationMoves, state.invocation.presence, left.source.presenceRow(left.index(i)))
						} else {
							correlation.presence = left.source.presenceRow(left.index(i))
						}
						if identity {
							correlation.queryOccurrences = correlation.queryOccurrences[:0]
							correlation.queryProvenance = correlation.queryProvenance[:0]
							if n.correlationPrefix > 0 {
								correlation.queryOccurrences = append(correlation.queryOccurrences, state.invocation.queryOccurrences...)
								correlation.queryProvenance = append(correlation.queryProvenance, state.invocation.queryProvenance...)
							}
							correlation.queryOccurrences = appendQueryOccurrences(correlation.queryOccurrences, left.source)
							correlation.queryProvenance = appendQueryProvenance(correlation.queryProvenance, left.source, left.index(i))
						}
						right, err = openQueryNode(ctx, &invocation, n.right)
						if err != nil {
							return false, err
						}
						if len(right.pending) != 0 {
							settled, failure := settlePendingQueryMarks(ctx, &invocation, n.right, right)
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
					nullFrame(n.leftWidth, n.rightWidth)
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
			if len(row) != n.rightWidth {
				return false, fmt.Errorf("stuntgres: invalid right join frame")
			}
			copy(frame[n.leftWidth:], row)
			if tail {
				return emit(ctx, out, absentQueryIndex, right.index(j))
			}
			evaluation.setContext(ctx)
			bindIdentity(left.index(leftOccurrence), right.index(j))
			fillJoinPresence(presence, presenceMoves, left.source.presenceRow(left.index(leftOccurrence)), right.source.presenceRow(right.index(j)))
			if n.predicate != nil {
				matches, err := updateMatches(&evaluation, n.predicate)
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
func initializeJoinEquality(ctx context.Context, expr BoundExpr, leftWidth int) error {
	if expr == nil {
		return nil
	}
	if logical, ok := expr.(logicalExpression); ok && logical.op == parser.LogicalAnd {
		if err := initializeJoinEquality(ctx, logical.left, leftWidth); err != nil {
			return err
		}
		return initializeJoinEquality(ctx, logical.right, leftWidth)
	}
	comparison, ok := expr.(comparisonExpression)
	if !ok || comparison.operator != "=" {
		return nil
	}
	side := func(expr BoundExpr) int {
		result := 0
		walkBoundExpression(expr, func(node BoundExpr) {
			column, ok := node.(columnExpression)
			if !ok || column.scope != 1 {
				return
			}
			value := 1
			if column.slot < leftWidth {
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
	left, right := side(comparison.left), side(comparison.right)
	if left*right != -1 {
		return nil
	}
	return comparison.predicate.DemandCollation(ctx)
}

// Captures retain their binding namespace and written origin while an enclosing
// join consumes locally satisfied references. Slots are relative to that join's
// inherited correlation frame, not to its output row.
type functionCorrelation struct {
	frame uint32
	slot  int
	name  string
	pos   parser.Position
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
