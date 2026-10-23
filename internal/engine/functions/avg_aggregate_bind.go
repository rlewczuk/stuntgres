package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindAvg(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var operand Argument
	var types []string
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
		types = make([]string, len(raw.Args))
	}
	// Transform every child before resolution, retaining only the unary operand.
	for i, child := range raw.Args {
		o, err := comparisonOperand(b, child)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = o
		}
		if types != nil {
			types[i] = DiagnosticNameOrUnknown(o.Usage.Type)
		}
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, operation, err := resolveAvgSignature(b, raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	typ := arg.ResultUsage().Type
	var provider AggregateProvider
	switch operation {
	case AggregateIntervalAvg:
		provider = NewIntervalAvg(typ.Usage())
	case AggregateNumericAvg:
		provider = NewNumericAvg(typ, b.Type(sqltype.Numeric).Usage())
	case AggregateFloatAvg:
		provider = NewFloatAvg(typ, b.Type(sqltype.Float8).Usage())
	}
	call := AggregateCall{Provider: provider, Arguments: []expr.Expr{arg}, Operation: operation}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, "avg", typ.ID())
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, b.Info(provider.ResultUsage(), raw.Pos, expr.Properties{}))
}

func resolveAvgSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operand Argument, types []string) (expr.Expr, AggregateOperation, error) {
	if len(raw.Args) > 100 {
		return nil, 0, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, 0, err
	}
	typ := operand.Usage.Type
	if len(raw.Args) == 1 {
		types = []string{DiagnosticNameOrUnknown(typ)}
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, 0, UnnotedResolutionError(b, name, raw.Pos, types, false)
	}
	if typ == nil {
		return nil, 0, aggregateUnknownError(name, raw.Pos)
	}
	var operation AggregateOperation
	switch typ {
	case b.Type(sqltype.Interval):
		operation = AggregateIntervalAvg
	case b.Type(sqltype.Int2), b.Type(sqltype.Int4), b.Type(sqltype.Int8), b.Type(sqltype.Numeric):
		operation = AggregateNumericAvg
	case b.Type(sqltype.Float4), b.Type(sqltype.Float8):
		operation = AggregateFloatAvg
	default:
		return nil, 0, UnnotedResolutionError(b, name, raw.Pos, types, false)
	}
	arg, err := finalizeOperand(b, operand, typ)
	return arg, operation, err
}

func windowAvg(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, plainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, operation, err := resolveAvgSignature(b, raw, name, firstOperand(operands), types)
	if err != nil {
		return arg, sqltype.Usage{}, err
	}
	switch operation {
	case AggregateIntervalAvg:
		return arg, arg.ResultUsage(), nil
	case AggregateNumericAvg:
		return arg, b.Type(sqltype.Numeric).Usage(), nil
	default:
		return arg, b.Type(sqltype.Float8).Usage(), nil
	}
}
