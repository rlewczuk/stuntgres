package engine

import (
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindAnyValue(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	// A valid single-argument call needs no operand/type slice allocation. Invalid
	// arities still transform every child before limit/namespace/signature checks.
	var singleType [1]string
	types := singleType[:]
	if len(raw.Args) != 1 {
		types = make([]string, len(raw.Args))
	}
	var operand functions.Argument[BoundExpr]
	for i, child := range raw.Args {
		bound, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operand = bound
		types[i] = "unknown"
		if typ := bound.Usage.Type; typ != nil {
			types[i] = typ.DiagnosticName()
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := b.resolveAnyValueSignature(raw, name, operand.Expression, operand.Usage, types)
	if err != nil {
		return nil, err
	}
	usage := arg.ResultUsage()
	usage.Typmod = -1
	call := aggregateCall{provider: functions.NewAnyValue(usage), argument: arg, operation: aggregateAnyValue}
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, "any_value", sqltype.ID(2283))
	if err != nil {
		return nil, err
	}
	properties := arg.Properties()
	info := b.info(usage, raw.Pos, ExprProperties{StageDependent: true, ParameterDependent: properties.ParameterDependent, Volatility: properties.Volatility})
	info.CollationConflict, info.CollationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	return b.finishAggregate(raw, call, filter, info)
}

func (b *expressionBinder) resolveAnyValueSignature(raw parser.AggregateExpr, name parser.ObjectName, argument BoundExpr, usage sqltype.Usage, types []string) (BoundExpr, error) {
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	switch functions.ResolveAnyValue(visible, raw.Star, len(raw.Args), usage) {
	case functions.Missing:
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	case functions.UnknownPolymorphic:
		return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
	}
	return argument, nil
}
