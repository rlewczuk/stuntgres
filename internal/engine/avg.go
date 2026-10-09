package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindAvg(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
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
	if len(raw.Args) == 1 {
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
	var provider aggregateProvider
	var operation aggregateOperation
	switch typ {
	case b.typ(sqltype.Interval):
		provider, operation = intervalAvgProvider{typ.Usage()}, aggregateIntervalAvg
	case b.typ(sqltype.Int2), b.typ(sqltype.Int4), b.typ(sqltype.Int8), b.typ(sqltype.Numeric):
		provider, operation = numericAvgProvider{typ, b.typ(sqltype.Numeric).Usage()}, aggregateNumericAvg
	case b.typ(sqltype.Float4), b.typ(sqltype.Float8):
		provider, operation = floatAvgProvider{typ, b.typ(sqltype.Float8).Usage()}, aggregateFloatAvg
	default:
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	arg, err := b.finalizeOperand(operand, typ)
	if err != nil {
		return nil, err
	}
	call := aggregateCall{provider: provider, argument: arg, operation: operation}
	slot := len(b.aggregates)
	if arg.Properties().Volatility == Immutable {
		for i, existing := range b.aggregates {
			if existing.operation == operation && b.sameGroupExpression(arg, existing.argument) {
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
	return aggregateExpression{expressionInfo: b.info(provider.ResultUsage(), raw.Pos, properties), slot: slot, frame: b.frameID()}, nil
}
