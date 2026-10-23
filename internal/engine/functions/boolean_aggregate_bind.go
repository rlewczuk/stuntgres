package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindBooleanAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	operands := make([]Argument, len(raw.Args))
	types := make([]string, len(raw.Args))
	for i, child := range raw.Args {
		operand, err := comparisonOperand(b, child)
		if err != nil {
			return nil, err
		}
		operands[i] = operand
		types[i] = DiagnosticNameOrUnknown(operand.Usage.Type)
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := resolveBooleanAggregateSignature(b, raw, name, operands, types)
	if err != nil {
		return nil, err
	}
	typ := arg.ResultUsage().Type
	operation := AggregateAnd
	if raw.Name == "bool_or" {
		operation = AggregateOr
	}
	call := AggregateCall{Provider: NewBooleanAggregateProvider(typ.Usage(), operation == AggregateAnd), Arguments: []expr.Expr{arg}, Operation: operation}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, raw.Name, sqltype.Bool)
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, b.Info(typ.Usage(), raw.Pos, expr.Properties{}))
}

// resolveBooleanAggregateSignature resolves and coerces the callable
// without registering ordinary aggregate state.
func resolveBooleanAggregateSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operands []Argument, types []string) (expr.Expr, error) {
	if len(operands) > 100 {
		return nil, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, err
	}
	typ := b.Type(sqltype.Bool)
	if !visible || raw.Star || len(operands) != 1 || (operands[0].Usage.Type != nil && operands[0].Usage.Type.BaseType() != typ) {
		return nil, UnnotedResolutionError(b, name, raw.Pos, types, false)
	}
	return coerceConditional(b, operands[0], typ.Usage(), raw.Name)
}

func windowBooleanAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, plainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, err := resolveBooleanAggregateSignature(b, raw, name, operands, types)
	if err != nil {
		return arg, sqltype.Usage{}, err
	}
	return arg, arg.ResultUsage(), nil
}
