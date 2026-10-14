package engine

import (
	"context"

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

func evaluateWindowValue(ctx context.Context, evaluation *EvalContext, child *indexedStage, call windowCall, rows []int, frame boundWindowFrame, head, tail, current, peerStart, peerEnd int) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	usage := call.expression.ResultUsage()
	ordinal := int64(0)
	if call.operation == windowNth {
		evaluation.bindQueryRow(child.source, child.index(rows[current]))
		value, err := call.ordinal.Eval(evaluation)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := validateProducedValue(value, call.ordinal.ResultUsage()); err != nil {
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
	member, present := windowValueMember(frame.exclusion, head, tail, current, peerStart, peerEnd, ordinal, call.operation == windowLast)
	if !present {
		return sqltype.Null(usage.Type), nil
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	evaluation.bindQueryRow(child.source, child.index(rows[member]))
	value, err := call.argument.Eval(evaluation)
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
