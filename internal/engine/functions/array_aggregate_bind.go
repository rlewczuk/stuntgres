package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindArrayAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var small [1]string
	var types []string
	if len(raw.Args) <= 1 {
		types = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		types = make([]string, len(raw.Args))
	}
	var operand Argument
	for i, child := range raw.Args {
		o, err := comparisonOperand(b, child)
		if err != nil {
			return nil, err
		}
		operand = o
		if types != nil {
			types[i] = b.DiagnosticTypeName(o.Usage.Type)
		}
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, usage, arrays, err := resolveArrayAggregateSignature(b, raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	properties := arg.Properties()
	properties.StageDependent = true
	info := b.Info(usage, raw.Pos, properties)
	info.CollationConflict, info.CollationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	operation := AggregateArrayScalar
	if arrays {
		operation = AggregateArrayArray
	}
	call := AggregateCall{
		Provider:     NewArrayAggregateProvider(arg.ResultUsage().Type, usage, arrays),
		Argument:     arg,
		Operation:    operation,
		IncludeNulls: true,
	}
	formal := arrayAggregateFormal(arg.ResultUsage().Type)
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, "array_agg", formal)
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, info)
}

func resolveArrayAggregateSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operand Argument, types []string) (expr.Expr, sqltype.Usage, bool, error) {
	if len(raw.Args) > 100 {
		return nil, sqltype.Usage{}, false, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, sqltype.Usage{}, false, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, sqltype.Usage{}, false, ResolutionError(name, raw.Pos, types, false)
	}
	if !operand.Usage.Type.Concrete() {
		return nil, sqltype.Usage{}, false, ResolutionError(name, raw.Pos, types, true)
	}
	arg := operand.Expression
	usage := arg.ResultUsage()
	usage.Typmod = -1
	arrays := arrayAggregateFormal(usage.Type) == 2277
	if arrays {
		usage.Type = usage.Type.BaseType()
		arg, err = coerceConditional(b, operand, usage, raw.Name)
		if err != nil {
			return nil, sqltype.Usage{}, false, err
		}
	} else {
		array, ok := b.ArrayType(usage.Type)
		if !ok || !array.Concrete() {
			return nil, sqltype.Usage{}, false, sqlError("42704", "could not find array type for data type "+b.DiagnosticTypeName(usage.Type))
		}
		usage.Type = array
	}
	return arg, usage, arrays, nil
}

func windowArrayAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, catalogTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, usage, _, err := resolveArrayAggregateSignature(b, raw, name, firstOperand(operands), types)
	return arg, usage, err
}
