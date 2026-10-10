package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindRangeBound(raw parser.FrameBound, order []sortKey) (boundRangeBound, error) {
	bound := boundRangeBound{kind: raw.Kind}
	if raw.Offset == nil {
		return bound, nil
	}
	old := b.aggregateClause
	aggregates, windows := b.aggregates, b.windowCalls
	b.aggregateClause = "window RANGE"
	defer func() { b.aggregateClause = old; b.aggregates = aggregates; b.windowCalls = windows }()
	operand, err := b.comparisonOperand(raw.Offset)
	if err != nil {
		return bound, err
	}
	support, usage, err := sqltype.BindRange(order[0].expression.ResultUsage(), operand.descriptor(), b.typ, b.diagnosticTypeName)
	if err != nil {
		if d, ok := err.(*sqlstate.Error); ok {
			copy := *d
			copy.Position = fmt.Sprint(raw.Offset.SourcePosition().Character)
			return bound, &copy
		}
		return bound, err
	}
	expr, err := b.finalizeOperand(operand, usage.Type)
	if err != nil {
		return bound, err
	}
	if expr.ResultUsage() != usage {
		plan, err := sqltype.PlanCoercion(expr.ResultUsage(), usage, sqltype.Implicit)
		if err != nil {
			return bound, err
		}
		expr = coercionExpression{b.info(usage, expr.SourcePosition(), coercionProperties(expr, plan)), expr, plan, sqltype.Implicit}
	}
	if expressionUsesFrame(expr, b.frameID()) {
		return bound, expressionError("42P10", "argument of RANGE must not contain variables", firstQueryBoundFrameVariable(expr, b.frameID()))
	}
	bound.offset, bound.support = expr, support
	return bound, nil
}
