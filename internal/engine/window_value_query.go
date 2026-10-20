package engine

import (
	"context"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Exclusions leave at most three ordered intervals. Count admitted rows, not
// nonnull values, without allocating or walking the whole frame for each result.
func windowValueMember(mode parser.FrameExclusion, head, tail, current, peerStart, peerEnd int, ordinal int64, reverse bool) (int, bool) {
	if head >= tail || ordinal < 0 {
		return 0, false
	}
	intervals := [3][2]int{{head, tail}}
	count := 1
	switch mode {
	case parser.ExcludeCurrentRow:
		intervals = [3][2]int{{head, min(tail, current)}, {max(head, current+1), tail}}
		count = 2
	case parser.ExcludeGroup:
		intervals = [3][2]int{{head, min(tail, peerStart)}, {max(head, peerEnd), tail}}
		count = 2
	case parser.ExcludeTies:
		intervals = [3][2]int{{head, min(tail, peerStart)}, {max(head, current), min(tail, current+1)}, {max(head, peerEnd), tail}}
		count = 3
	}
	for step := range count {
		index := step
		if reverse {
			index = count - 1 - step
		}
		start, end := intervals[index][0], intervals[index][1]
		length := int64(max(0, end-start))
		if ordinal < length {
			if reverse {
				return end - 1 - int(ordinal), true
			}
			return start + int(ordinal), true
		}
		ordinal -= length
	}
	return 0, false
}

func evaluateWindowValue(ctx context.Context, evaluation *EvalContext, child *indexedStage, call plan.WindowCall, rows []int, frame plan.WindowFrame, head, tail, current, peerStart, peerEnd int) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	usage := call.Expression.ResultUsage()
	ordinal := int64(0)
	if call.Operation == plan.WindowNth {
		evaluation.bindQueryRow(child.source, child.index(rows[current]))
		value, err := call.Ordinal.Eval(evaluation)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := validateProducedValue(value, call.Ordinal.ResultUsage()); err != nil {
			return sqltype.Value{}, err
		}
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(usage.Type), nil
		}
		ordinal, err = value.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if ordinal <= 0 {
			return sqltype.Value{}, sqlError("22016", "argument of nth_value must be greater than zero")
		}
		ordinal--
	}
	member, present := windowValueMember(frame.Exclusion, head, tail, current, peerStart, peerEnd, ordinal, call.Operation == plan.WindowLast)
	if !present {
		return sqltype.Null(usage.Type), nil
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	evaluation.bindQueryRow(child.source, child.index(rows[member]))
	value, err := call.Argument.Eval(evaluation)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := validateProducedValue(value, usage); err != nil {
		return sqltype.Value{}, err
	}
	// Value owns immutable payloads; retaining it in the output overlay neither
	// aliases a reusable scan buffer nor reevaluates/caches the selected member.
	return value, ctx.Err()
}

// A sort-input boundary retains exact required targets before matching window
// leaves from postponed volatile payloads. Only an ACTUAL whole retained target
// can own a leaf; unmatched postponed leaves keep independent execution slots.
func (b *expressionBinder) windowValueAliases(calls []plan.WindowCall, projection []expr.Expr, order, on []plan.SortKey) []int {
	if len(order) == 0 {
		return nil
	}
	hasValues := false
	for _, call := range calls {
		hasValues = hasValues || call.Operation.EvaluatesArgument()
	}
	if !hasValues {
		return nil
	}
	demanded := func(output int) bool {
		for _, keys := range [2][]plan.SortKey{order, on} {
			for _, key := range keys {
				if key.OutputOrdinal == output+1 || key.TargetIdentity == output+1 {
					return true
				}
			}
		}
		return false
	}
	owners := make([]*plan.WindowExpr, 0, len(calls))
	retain := func(expression expr.Expr) {
		leaf, ok := expression.(*plan.WindowExpr)
		if !ok || leaf.Frame != b.frameID() || leaf.Call < 0 || leaf.Call >= len(calls) || !calls[leaf.Call].Operation.EvaluatesArgument() {
			return
		}
		for _, owner := range owners {
			if owner.Call == leaf.Call {
				return
			}
		}
		owners = append(owners, leaf)
	}
	for i, expression := range projection {
		if demanded(i) || expression.Properties().Volatility != expr.Volatile {
			retain(expression)
		}
	}
	for _, keys := range [2][]plan.SortKey{order, on} {
		for _, key := range keys {
			retain(key.Expression)
		}
	}
	var aliases []int
	for i, expression := range projection {
		if demanded(i) || expression.Properties().Volatility != expr.Volatile {
			continue
		}
		walkBoundExpression(expression, func(child expr.Expr) {
			leaf, ok := child.(*plan.WindowExpr)
			if !ok || leaf.Frame != b.frameID() || leaf.Call < 0 || leaf.Call >= len(calls) || !calls[leaf.Call].Operation.EvaluatesArgument() {
				return
			}
			for _, owner := range owners {
				if !b.sameWindowCall(calls[leaf.Call], calls[owner.Call]) {
					continue
				}
				if leaf.Call != owner.Call {
					if aliases == nil {
						aliases = make([]int, len(calls))
						for j := range aliases {
							aliases[j] = -1
						}
					}
					aliases[leaf.Call] = owner.Call
				}
				return
			}
		})
	}
	return aliases
}
