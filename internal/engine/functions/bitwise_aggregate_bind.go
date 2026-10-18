package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindBitwiseAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var operand Argument
	var unary [1]string
	var types []string
	if len(raw.Args) == 1 {
		types = unary[:]
	} else if len(raw.Args) <= 100 {
		types = make([]string, len(raw.Args))
	}
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
	arg, err := resolveBitwiseAggregateSignature(b, raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	operation := AggregateBitAnd
	if raw.Name == "bit_or" {
		operation = AggregateBitOr
	}
	if raw.Name == "bit_xor" {
		operation = AggregateBitXor
	}
	usage := arg.ResultUsage().Type.Usage()
	operator, err := bitwiseAggregateOperator(operation)
	if err != nil {
		return nil, err
	}
	var provider AggregateProvider
	if usage.Type.ID() == sqltype.Bit {
		provider = NewBitStringAggregateProvider(usage, operator)
	} else {
		bound, err := sqltype.BindBitwise(operator, usage.Type, usage.Type)
		if err != nil {
			return nil, err
		}
		provider = NewIntegralBitwiseAggregateProvider(usage, operator, bound)
	}
	call := AggregateCall{Provider: provider, Argument: arg, Operation: operation}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, raw.Name, usage.Type.ID())
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, b.Info(usage, raw.Pos, expr.Properties{}))
}

// resolveBitwiseAggregateSignature resolves and coerces the callable; window
// calls share it and never build a provider.
func resolveBitwiseAggregateSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operand Argument, types []string) (expr.Expr, error) {
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
		return nil, ResolutionError(name, raw.Pos, types, true)
	}
	typ = typ.BaseType()
	switch typ.ID() {
	case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Bit:
	case sqltype.VarBit:
		typ = b.Type(sqltype.Bit)
	default:
		return nil, sumSignatureError(name, raw.Pos, types, len(raw.Order))
	}
	return coerceConditional(b, operand, typ.Usage(), raw.Name)
}

func bitwiseAggregateOperator(operation AggregateOperation) (sqltype.BitwiseOperator, error) {
	switch operation {
	case AggregateBitAnd:
		return sqltype.BitwiseAnd, nil
	case AggregateBitOr:
		return sqltype.BitwiseOr, nil
	case AggregateBitXor:
		return sqltype.BitwiseXor, nil
	default:
		return 0, fmt.Errorf("stuntgres: invalid bitwise aggregate operation")
	}
}

func windowBitwiseAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, catalogTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, err := resolveBitwiseAggregateSignature(b, raw, name, firstOperand(operands), types)
	if err != nil {
		return arg, sqltype.Usage{}, err
	}
	return arg, arg.ResultUsage().Type.Usage(), nil
}
