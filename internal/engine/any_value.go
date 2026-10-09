package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type anyValueProvider struct{ usage sqltype.Usage }
type anyValueState struct{ value sqltype.Value }

func (p anyValueProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p anyValueProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.usage.Type.Concrete() {
		return nil, fmt.Errorf("stuntgres: invalid any_value result type")
	}
	return &anyValueState{value: sqltype.Null(p.usage.Type)}, nil
}
func (p anyValueProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*anyValueState)
	if !ok || s == nil || !p.usage.Type.Concrete() || s.value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid any_value state")
	}
	// Validate later candidates too. Retention does not end argument evaluation
	// or relax the execution's concrete descriptor ownership contract.
	if value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid any_value datum")
	}
	if s.value.IsNull() && !value.IsNull() {
		s.value = value
	}
	return ctx.Err()
}
func (p anyValueProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*anyValueState)
	if !ok || s == nil || !p.usage.Type.Concrete() || s.value.Type() != p.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid any_value state")
	}
	return s.value, nil
}

func (b *expressionBinder) bindAnyValue(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	// A valid single-argument call needs no operand/type slice allocation. Invalid
	// arities still transform every child before limit/namespace/signature checks.
	var singleType [1]string
	types := singleType[:]
	if len(raw.Args) != 1 {
		types = make([]string, len(raw.Args))
	}
	var operand comparisonOperand
	for i, child := range raw.Args {
		bound, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		operand = bound
		types[i] = "unknown"
		if typ := bound.descriptor(); typ != nil {
			types[i] = typ.DiagnosticName()
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	if !operand.descriptor().Concrete() {
		return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
	}
	arg := operand.node
	usage := arg.ResultUsage()
	usage.Typmod = -1
	call := aggregateCall{provider: anyValueProvider{usage}, argument: arg, operation: aggregateAnyValue}
	slot := len(b.aggregates)
	if arg.Properties().Volatility == Immutable {
		for i, existing := range b.aggregates {
			if existing.operation == aggregateAnyValue && b.sameGroupExpression(arg, existing.argument) {
				slot = i
				break
			}
		}
	}
	if slot == len(b.aggregates) {
		b.aggregates = append(b.aggregates, call)
	}
	properties := arg.Properties()
	info := b.info(usage, raw.Pos, ExprProperties{StageDependent: true, ParameterDependent: properties.ParameterDependent, Volatility: properties.Volatility})
	info.collationConflict, info.collationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	return aggregateExpression{expressionInfo: info, slot: slot, frame: b.frameID()}, nil
}
