package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type intervalAvgProvider struct{ usage sqltype.Usage }

func (p intervalAvgProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p intervalAvgProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := &sqltype.IntervalAvgState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p intervalAvgProvider) Advance(ctx context.Context, state any, value sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*sqltype.IntervalAvgState)
	if !ok || s == nil {
		return fmt.Errorf("stuntgres: invalid interval average state")
	}
	if value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid interval average datum")
	}
	if err := s.Add(value); err != nil {
		return err
	}
	return ctx.Err()
}
func (p intervalAvgProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*sqltype.IntervalAvgState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid interval average state")
	}
	value, err := s.Result(p.usage.Type)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}

func (b *expressionBinder) bindIntervalAvg(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var operand comparisonOperand
	var types []string
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
		types = make([]string, len(raw.Args))
	}
	// Transform every child before resolution, retaining only the unary operand.
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = o
		}
		if types != nil {
			types[i] = "unknown"
			if typ := o.descriptor(); typ != nil {
				types[i] = typ.DiagnosticName()
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	typ := operand.descriptor()
	if len(raw.Args) == 1 && (!visible || raw.Star || typ != b.typ(sqltype.Interval)) {
		types = []string{"unknown"}
		if typ != nil {
			types[0] = typ.DiagnosticName()
		}
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	if typ == nil {
		d := expressionError("42725", fmt.Sprintf("function %s(unknown) is not unique", objectNameText(name)), raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, d
	}
	if typ != b.typ(sqltype.Interval) {
		switch typ.ID() {
		case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8:
			return nil, expressionError("0A000", "numeric average aggregates are unsupported", raw.Pos)
		}
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	arg, err := b.finalizeOperand(operand, typ)
	if err != nil {
		return nil, err
	}
	if inner := aggregateOccurrence(arg); inner != nil {
		return nil, expressionError("42803", "aggregate function calls cannot be nested", inner.pos)
	}
	usage := typ.Usage()
	call := aggregateCall{provider: intervalAvgProvider{usage}, argument: arg, operation: aggregateIntervalAvg}
	slot := len(b.aggregates)
	if arg.Properties().Volatility == Immutable {
		for i, existing := range b.aggregates {
			if existing.operation == aggregateIntervalAvg && b.sameGroupExpression(arg, existing.argument) {
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
	return aggregateExpression{b.info(usage, raw.Pos, properties), slot}, nil
}
