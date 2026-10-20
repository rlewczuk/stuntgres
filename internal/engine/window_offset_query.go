package engine

import (
	"context"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Widen before negating the signed INT4 offset, then check the available
// distance before adding. This remains safe for conceptual MaxInt partitions.
func WindowOffsetTarget(size, current int, offset int32, reverse bool) (int, bool) {
	delta := int64(offset)
	if reverse {
		delta = -delta
	}
	if delta >= 0 {
		if delta >= int64(size-current) {
			return 0, false
		}
		return current + int(delta), true
	}
	distance := -delta
	if distance > int64(current) {
		return 0, false
	}
	return current - int(distance), true
}

func evaluateWindowOffset(ctx context.Context, evaluation *EvalContext, child *indexedStage, call plan.WindowCall, rows []int, current int) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	usage := call.Expression.ResultUsage()
	evaluation.BindQueryRow(child.Source, child.Index(rows[current]))
	offset := int64(1)
	if call.Ordinal != nil {
		value, err := call.Ordinal.Eval(evaluation)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := ValidateProducedValue(value, call.Ordinal.ResultUsage()); err != nil {
			return sqltype.Value{}, err
		}
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(usage.Type), nil
		}
		offset, err = value.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	member, present := WindowOffsetTarget(len(rows), current, int32(offset), call.Operation == plan.WindowLag)
	expression := call.DefaultValue
	if present {
		evaluation.BindQueryRow(child.Source, child.Index(rows[member]))
		expression = call.Argument
	}
	if expression == nil {
		return sqltype.Null(usage.Type), nil
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	value, err := expression.Eval(evaluation)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ValidateProducedValue(value, usage); err != nil {
		return sqltype.Value{}, err
	}
	return value, ctx.Err()
}
