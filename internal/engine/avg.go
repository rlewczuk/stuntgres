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
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, operation, err := b.resolveAvgSignature(raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	typ := arg.ResultUsage().Type
	var provider aggregateProvider
	switch operation {
	case aggregateIntervalAvg:
		provider = intervalAvgProvider{typ.Usage()}
	case aggregateNumericAvg:
		provider = numericAvgProvider{typ, b.typ(sqltype.Numeric).Usage()}
	case aggregateFloatAvg:
		provider = floatAvgProvider{typ, b.typ(sqltype.Float8).Usage()}
	}
	call := aggregateCall{provider: provider, argument: arg, operation: operation}
	return b.finishAggregate(raw, call, filter, b.info(provider.ResultUsage(), raw.Pos, ExprProperties{}))
}

func (b *expressionBinder) resolveAvgSignature(raw parser.AggregateExpr, name parser.ObjectName, operand comparisonOperand, types []string) (BoundExpr, aggregateOperation, error) {
	if len(raw.Args) > 100 {
		return nil, 0, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, 0, err
	}
	typ := operand.descriptor()
	if len(raw.Args) == 1 {
		types = []string{"unknown"}
		if typ != nil {
			types[0] = typ.DiagnosticName()
		}
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, 0, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	if typ == nil {
		d := expressionError("42725", fmt.Sprintf("function %s(unknown) is not unique", objectNameText(name)), raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, 0, d
	}
	var operation aggregateOperation
	switch typ {
	case b.typ(sqltype.Interval):
		operation = aggregateIntervalAvg
	case b.typ(sqltype.Int2), b.typ(sqltype.Int4), b.typ(sqltype.Int8), b.typ(sqltype.Numeric):
		operation = aggregateNumericAvg
	case b.typ(sqltype.Float4), b.typ(sqltype.Float8):
		operation = aggregateFloatAvg
	default:
		return nil, 0, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	arg, err := b.finalizeOperand(operand, typ)
	return arg, operation, err
}
