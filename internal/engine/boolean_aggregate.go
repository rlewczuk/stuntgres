package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindBooleanAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	operands := make([]comparisonOperand, len(raw.Args))
	types := make([]string, len(raw.Args))
	for i, child := range raw.Args {
		operand, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		operands[i] = operand
		types[i] = "unknown"
		if typ := operand.descriptor(); typ != nil {
			types[i] = typ.DiagnosticName()
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := b.resolveBooleanAggregateSignature(raw, name, operands, types)
	if err != nil {
		return nil, err
	}
	typ := arg.ResultUsage().Type
	operation := aggregateAnd
	if raw.Name == "bool_or" {
		operation = aggregateOr
	}
	call := aggregateCall{provider: functions.NewBooleanAggregateProvider(typ.Usage(), operation == aggregateAnd), argument: arg, operation: operation}
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, raw.Name, sqltype.Bool)
	if err != nil {
		return nil, err
	}
	return b.finishAggregate(raw, call, filter, b.info(typ.Usage(), raw.Pos, ExprProperties{}))
}

// Resolve/coerce the callable without registering ordinary aggregate state.
func (b *expressionBinder) resolveBooleanAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operands []comparisonOperand, types []string) (BoundExpr, error) {
	if len(operands) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	typ := b.typ(sqltype.Bool)
	if !visible || raw.Star || len(operands) != 1 || (operands[0].descriptor() != nil && operands[0].descriptor().BaseType() != typ) {
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	return b.coerceConditional(operands[0], typ.Usage(), raw.Name)
}
