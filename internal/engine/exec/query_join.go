package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func OpenJoinNode(ctx context.Context, state *State, n *plan.JoinNode) (Stage, error) {
	foreignOwner := n // Private coercion rewrites must not create new scan owners.
	// Admission/preflight is independent of operand population. Opening both
	// children must validate their row-independent coercions even when the
	// other operand yields no candidate; this does not produce either child.
	if n.Predicate != nil {
		evaluation := state.Evaluation(ctx)
		predicate, changed, err := PreflightCoercions(&evaluation, n.Predicate)
		if err != nil {
			return Stage{}, err
		}
		if changed {
			copy := *n
			copy.Predicate = predicate
			n = &copy
		}
	}
	if err := initializeJoinEquality(ctx, n.Predicate, n.LeftWidth); err != nil {
		return Stage{}, err
	}
	left, err := OpenNode(ctx, state, n.Left)
	if err != nil {
		return Stage{}, err
	}
	right := Stage{Source: &Source{FlatWidth: n.RightWidth, Materialized: true, Authority: state.Source.Authority, Host: state.Source.Host}, Columns: n.Right.Schema(), Selection: []int{}}
	if n.CorrelatedRight {
		establishDeferredPresence(right.Source, n.Right)
	} else {
		right, err = OpenNode(ctx, state, n.Right)
		if err != nil {
			left.Close()
			return Stage{}, err
		}
	}
	width := len(n.Schema())
	if n.LeftWidth < 0 || n.RightWidth < 0 || n.LeftWidth > width || n.RightWidth > width-n.LeftWidth || len(n.Merges) != width-n.LeftWidth-n.RightWidth {
		left.Close()
		right.Close()
		return Stage{}, fmt.Errorf("stuntgres: invalid join frame width")
	}
	if n.Kind != parser.JoinInner {
		settledLeft, failure := SettlePendingQueryMarks(ctx, state, n.Left, left)
		if failure != nil {
			left.Close()
			right.Close()
			return Stage{}, failure
		}
		left = settledLeft
		if !n.CorrelatedRight {
			settledRight, failure := SettlePendingQueryMarks(ctx, state, n.Right, right)
			if failure != nil {
				left.Close()
				right.Close()
				return Stage{}, failure
			}
			right = settledRight
		}
	}
	if n.CorrelatedRight && (n.Kind == parser.JoinRight || n.Kind == parser.JoinFull || n.CorrelationPrefix < 0) {
		left.Close()
		right.Close()
		return Stage{}, fmt.Errorf("stuntgres: invalid correlated join layout")
	}
	if n.CorrelatedRight && n.CorrelationPrefix > 0 && (state.Invocation == nil || state.Invocation.Scope != 1 || state.Invocation.Frame != n.Frame || len(state.Invocation.Row) < n.CorrelationPrefix) {
		left.Close()
		right.Close()
		return Stage{}, fmt.Errorf("stuntgres: missing inherited function invocation frame")
	}
	var correlationRow []sqltype.Value
	var correlationMoves []presenceCopy
	var correlationPresence []bool
	correlation := state.Evaluation(nil)
	if n.CorrelatedRight {
		correlationRow = make([]sqltype.Value, n.CorrelationPrefix+n.LeftWidth)
		correlation.Frame = n.Frame
		correlation.Row = correlationRow
		correlation.PresenceSlots = left.Source.PresenceSlots
		if n.CorrelationPrefix > 0 {
			// Prefix and local-left slots share the bound query frame but
			// may have different packed indices. Compile their union once.
			prefix := Source{PresenceSlots: state.Invocation.PresenceSlots}
			layout := Source{}
			correlationMoves, correlationPresence = joinPresence(&layout, &prefix, left.Source, 0)
			correlation.PresenceSlots = layout.PresenceSlots
			correlation.Presence = correlationPresence
		}
	}
	invocation := *state
	invocation.foreignOwner = foreignOwner
	if n.CorrelatedRight {
		invocation.SourceMode = state.subquerySourceMode()
		invocation.Invocation = &correlation
	}
	rightIdentity := false
	frame := make([]sqltype.Value, width)
	source := &Source{FlatWidth: width, Materialized: true, Authority: state.Source.Authority, Host: state.Source.Host}
	presenceMoves, presence := joinPresence(source, left.Source, right.Source, n.PresenceSlot)
	locking := state.Locking || state.Source.Locks.RowMarked()
	identity := locking || state.VirtualReads
	if identity {
		source.Occurrences = AppendQueryOccurrences(source.Occurrences, left.Source)
		source.Occurrences = AppendQueryOccurrences(source.Occurrences, right.Source)
	}
	if locking && state.Source.Locks != nil && !n.CorrelatedRight {
		source.JoinLeft, source.JoinRight = left.Source, right.Source
	}
	out := Stage{Source: source, Columns: n.Schema(), Selection: []int{}}
	if len(left.Pending)+len(right.Pending) > 0 {
		out.Pending = make([]queryPendingMark, len(left.Pending)+len(right.Pending))
		copy(out.Pending, left.Pending)
		copy(out.Pending[len(left.Pending):], right.Pending)
	}
	out.Finish = func() error {
		err := left.Close()
		other := right.Close()
		if err != nil {
			return err
		}
		return other
	}
	evaluation := state.Evaluation(nil)
	evaluation.Row = frame
	if identity {
		evaluation.QueryOccurrences = source.Occurrences
		evaluation.QueryProvenance = make([]int, 0, len(source.Occurrences))
	}
	bindIdentity := func(li, ri int) {
		if identity {
			evaluation.QueryProvenance = AppendQueryProvenance(evaluation.QueryProvenance[:0], left.Source, li)
			evaluation.QueryProvenance = AppendQueryProvenance(evaluation.QueryProvenance, right.Source, ri)
		}
	}
	evaluation.PresenceSlots, evaluation.Presence = source.PresenceSlots, presence
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
	emit := func(ctx context.Context, out *Stage, li, ri int) (bool, error) {
		evaluation.setContext(ctx)
		bindIdentity(li, ri)
		fillJoinPresence(presence, presenceMoves, left.Source.PresenceRow(li), right.Source.PresenceRow(ri))
		for k, expr := range n.Merges {
			value, err := expr.Eval(&evaluation)
			if err != nil {
				return false, err
			}
			if err := ValidateProducedValue(value, n.Schema()[n.LeftWidth+n.RightWidth+k].Usage); err != nil {
				return false, err
			}
			frame[n.LeftWidth+n.RightWidth+k] = value
		}
		maxInt := int(^uint(0) >> 1)
		if source.FlatCount == maxInt || len(source.Flat) > maxInt-width || len(source.presenceFlat) > maxInt-len(presence) {
			return false, fmt.Errorf("stuntgres: join result is too large")
		}
		source.Flat = append(source.Flat, frame...)
		source.presenceFlat = append(source.presenceFlat, presence...)
		if identity {
			source.Provenance = append(source.Provenance, evaluation.QueryProvenance...)
		}
		if locking && state.Source.Locks != nil {
			source.Versions = AppendQueryVersions(source.Versions, left.Source, li, state.Source.Locks)
			source.Versions = AppendQueryVersions(source.Versions, right.Source, ri, state.Source.Locks)
			if !n.CorrelatedRight {
				source.JoinPairs = append(source.JoinPairs, QueryPair{Left: li, Right: ri})
			}
		}
		if identity && n.CorrelatedRight {
			source.invocationLineage = append(source.invocationLineage, queryInvocationLineage{left.Source, right.Source, li, ri})
		}
		out.Selection = append(out.Selection, source.FlatCount)
		source.FlatCount++
		return true, ctx.Err()
	}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		for {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if !tail && leftOccurrence < 0 {
				i, present, err := left.NextOccurrence(ctx)
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
					row := left.Source.Row(left.Index(i))
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
							copy(correlationRow, state.Invocation.Row[:n.CorrelationPrefix])
						}
						copy(correlationRow[n.CorrelationPrefix:], row)
						if n.CorrelationPrefix > 0 {
							fillJoinPresence(correlationPresence, correlationMoves, state.Invocation.Presence, left.Source.PresenceRow(left.Index(i)))
						} else {
							correlation.Presence = left.Source.PresenceRow(left.Index(i))
						}
						if identity {
							correlation.QueryOccurrences = correlation.QueryOccurrences[:0]
							correlation.QueryProvenance = correlation.QueryProvenance[:0]
							if n.CorrelationPrefix > 0 {
								correlation.QueryOccurrences = append(correlation.QueryOccurrences, state.Invocation.QueryOccurrences...)
								correlation.QueryProvenance = append(correlation.QueryProvenance, state.Invocation.QueryProvenance...)
							}
							correlation.QueryOccurrences = AppendQueryOccurrences(correlation.QueryOccurrences, left.Source)
							correlation.QueryProvenance = AppendQueryProvenance(correlation.QueryProvenance, left.Source, left.Index(i))
						}
						right, err = OpenNode(ctx, &invocation, n.Right)
						if err != nil {
							return false, err
						}
						if len(right.Pending) != 0 {
							settled, failure := SettlePendingQueryMarks(ctx, &invocation, n.Right, right)
							if failure != nil {
								right.Close()
								return false, failure
							}
							right = settled
						}
						if identity && !rightIdentity {
							source.Occurrences = AppendQueryOccurrences(source.Occurrences, right.Source)
							evaluation.QueryOccurrences = source.Occurrences
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
				li := left.Index(leftOccurrence)
				leftOccurrence = -1
				if preserveLeft && !leftMatched {
					nullFrame(n.LeftWidth, n.RightWidth)
					return emit(ctx, out, li, AbsentQueryIndex)
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
			row := right.Source.Row(right.Index(j))
			if len(row) != n.RightWidth {
				return false, fmt.Errorf("stuntgres: invalid right join frame")
			}
			copy(frame[n.LeftWidth:], row)
			if tail {
				return emit(ctx, out, AbsentQueryIndex, right.Index(j))
			}
			evaluation.setContext(ctx)
			bindIdentity(left.Index(leftOccurrence), right.Index(j))
			fillJoinPresence(presence, presenceMoves, left.Source.PresenceRow(left.Index(leftOccurrence)), right.Source.PresenceRow(right.Index(j)))
			if n.Predicate != nil {
				matches, err := UpdateMatches(&evaluation, n.Predicate)
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
			return emit(ctx, out, left.Index(leftOccurrence), right.Index(j))
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
		plan.WalkExpression(bound, func(node expr.Expr) {
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
	left, right           *Source
	leftIndex, rightIndex int
}

func setQueryInvocationLineage(source *Source, index int) {
	if index == AbsentQueryIndex {
		if len(source.invocationLineage) != 0 {
			lineage := source.invocationLineage[0]
			SetQueryLineage(lineage.left, index)
			SetQueryLineage(lineage.right, index)
		}
		return
	}
	lineage := source.invocationLineage[index]
	SetQueryLineage(lineage.left, lineage.leftIndex)
	SetQueryLineage(lineage.right, lineage.rightIndex)
}

func UpdateMatches(eval *Context, predicate expr.Expr) (bool, error) {
	if predicate == nil {
		return true, eval.Context.Err()
	}
	value, err := predicate.Eval(eval)
	if err != nil {
		return false, err
	}
	truth, err := plan.TruthOf(value)
	return truth == plan.True, err
}
