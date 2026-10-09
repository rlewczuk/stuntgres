package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type booleanAggregateProvider struct {
	usage     sqltype.Usage
	operation aggregateOperation
}
type booleanAggregateState struct{ seen, value bool }

func (p booleanAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p booleanAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &booleanAggregateState{}, nil
}
func (p booleanAggregateProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*booleanAggregateState)
	if !ok {
		return fmt.Errorf("stuntgres: invalid Boolean aggregate state")
	}
	v, err := value.Boolean()
	if err != nil || value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid Boolean aggregate datum")
	}
	if !s.seen {
		s.seen, s.value = true, v
	} else if p.operation == aggregateAnd {
		s.value = s.value && v
	} else {
		s.value = s.value || v
	}
	return ctx.Err()
}
func (p booleanAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*booleanAggregateState)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid Boolean aggregate state")
	}
	if !s.seen {
		return sqltype.Null(p.usage.Type), nil
	}
	return sqltype.Boolean(p.usage.Type, s.value), nil
}

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
	arg, err := b.coerceConditional(operands[0], typ.Usage(), raw.Name)
	if err != nil {
		return nil, err
	}
	if inner := aggregateOccurrence(arg); inner != nil {
		return nil, expressionError("42803", "aggregate function calls cannot be nested", inner.pos)
	}
	operation := aggregateAnd
	if raw.Name == "bool_or" {
		operation = aggregateOr
	}
	call := aggregateCall{provider: booleanAggregateProvider{typ.Usage(), operation}, argument: arg, operation: operation}
	slot := len(b.aggregates)
	if arg.Properties().Volatility == Immutable {
		for i, existing := range b.aggregates {
			if operation == existing.operation && b.sameGroupExpression(arg, existing.argument) {
				slot = i
				break
			}
		}
	}
	if slot == len(b.aggregates) {
		b.aggregates = append(b.aggregates, call)
	}
	p := arg.Properties()
	properties := ExprProperties{StageDependent: true, ParameterDependent: p.ParameterDependent, Volatility: p.Volatility}
	return aggregateExpression{b.info(typ.Usage(), raw.Pos, properties), slot}, nil
}
