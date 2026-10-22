package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindAnyValue(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	// A valid single-argument call needs no operand/type slice allocation. Invalid
	// arities still transform every child before limit/namespace/signature checks.
	var singleType [1]string
	types := singleType[:]
	if len(raw.Args) != 1 {
		types = make([]string, len(raw.Args))
	}
	var operand Argument
	for i, child := range raw.Args {
		bound, err := comparisonOperand(b, child)
		if err != nil {
			return nil, err
		}
		operand = bound
		types[i] = DiagnosticNameOrUnknown(bound.Usage.Type)
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := ResolveAnyValueSignature(b, raw, name, operand.Expression, operand.Usage, types)
	if err != nil {
		return nil, err
	}
	usage := arg.ResultUsage()
	usage.Typmod = -1
	call := AggregateCall{Provider: NewAnyValue(usage), Argument: arg, Operation: AggregateAnyValue}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, "any_value", sqltype.ID(2283))
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, inputCollatedInfo(b, usage, raw.Pos, arg))
}

// ResolveAnyValueSignature resolves any_value(anyelement); the JSON array
// aggregates share its polymorphic resolution.
func ResolveAnyValueSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, argument expr.Expr, usage sqltype.Usage, types []string) (expr.Expr, error) {
	if len(raw.Args) > 100 {
		return nil, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, err
	}
	switch ResolveAnyValue(visible, raw.Star, len(raw.Args), usage) {
	case Missing:
		return nil, UnnotedResolutionError(b, name, raw.Pos, types, false)
	case UnknownPolymorphic:
		return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
	}
	return argument, nil
}

func windowAnyValue(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, plainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	operand := firstOperand(operands)
	arg, err := ResolveAnyValueSignature(b, raw, name, operand.Expression, sqltype.Usage{Type: operand.Usage.Type}, types)
	if err != nil {
		return arg, sqltype.Usage{}, err
	}
	usage := arg.ResultUsage()
	usage.Typmod = -1
	return arg, usage, nil
}
