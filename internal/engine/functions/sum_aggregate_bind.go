package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func bindSum(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var operand Argument
	var singleType [1]string
	var types []string
	if len(raw.Args) == 1 {
		types = singleType[:]
	} else if len(raw.Args) <= 100 {
		types = make([]string, len(raw.Args))
	}
	// Children and FILTER are analyzed before signature resolution.
	for i, child := range raw.Args {
		o, err := comparisonOperand(b, child)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = o
		}
		if types != nil {
			types[i] = b.DiagnosticTypeName(o.Usage.Type)
		}
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := resolveSumSignature(b, raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	provider, operation, err := SumProvider(b, arg.ResultUsage().Type)
	if err != nil {
		return nil, err
	}
	call := AggregateCall{Provider: provider, Arguments: []expr.Expr{arg}, Operation: operation}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, "sum", arg.ResultUsage().Type.ID())
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, b.Info(provider.ResultUsage(), raw.Pos, expr.Properties{}))
}

// SumProvider selects the SUM algorithm for a resolved input type. Window
// SUM shares it for its numeric inputs.
func SumProvider(b Binder, typ *sqltype.Descriptor) (AggregateProvider, AggregateOperation, error) {
	switch typ.ID() {
	case sqltype.Int2, sqltype.Int4:
		return NewIntegralSum(typ, b.Type(sqltype.Int8).Usage()), AggregateIntegralSum, nil
	case sqltype.Int8, sqltype.Numeric:
		return NewNumericSum(typ, b.Type(sqltype.Numeric).Usage()), AggregateNumericSum, nil
	case sqltype.Float4, sqltype.Float8:
		return NewFloatSum(typ, typ.Usage()), AggregateFloatSum, nil
	case sqltype.Money:
		bound, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, typ, typ, typ)
		if err != nil {
			return nil, AggregateMoneySum, err
		}
		return NewMoneySum(typ.Usage(), bound), AggregateMoneySum, nil
	case sqltype.Interval:
		return NewIntervalSum(typ.Usage()), AggregateIntervalSum, nil
	}
	return nil, AggregateCount, nil
}

// resolveSumSignature resolves and coerces SUM's callable without building a
// provider or registering aggregation; window SUM shares it.
func resolveSumSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operand Argument, types []string) (expr.Expr, error) {
	if len(raw.Args) > 100 {
		return nil, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, sumSignatureError(name, raw.Pos, types, len(raw.Order))
	}
	typ := operand.Usage.Type
	if typ == nil {
		return nil, aggregateUnknownError(name, raw.Pos)
	}
	typ = typ.BaseType()
	switch typ.ID() {
	case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8, sqltype.Money, sqltype.Interval:
		return coerceConditional(b, operand, typ.Usage(), raw.Name)
	default:
		return nil, sumSignatureError(name, raw.Pos, types, len(raw.Order))
	}
}

// sumSignatureError is the missing-signature error of single-argument
// aggregates, with PostgreSQL's hint for an ORDER BY placed before an
// argument separator.
func sumSignatureError(name parser.ObjectName, pos parser.Position, types []string, orderKeys int) error {
	d := ResolutionError(name, pos, types, false).(*sqlstate.Error)
	if orderKeys > 1 {
		d.Hint = "No aggregate function matches the given name and argument types. Perhaps you misplaced ORDER BY; ORDER BY must appear after all regular arguments of the aggregate."
	}
	return d
}

func windowSum(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, catalogTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, err := resolveSumSignature(b, raw, name, firstOperand(operands), types)
	if err != nil {
		return arg, sqltype.Usage{}, err
	}
	switch arg.ResultUsage().Type.ID() {
	case sqltype.Int2, sqltype.Int4:
		return arg, b.Type(sqltype.Int8).Usage(), nil
	case sqltype.Int8, sqltype.Numeric:
		return arg, b.Type(sqltype.Numeric).Usage(), nil
	default:
		return arg, arg.ResultUsage(), nil
	}
}
