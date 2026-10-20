package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindWindowBound(raw parser.FrameBound, order []plan.SortKey, unit parser.FrameUnit) (plan.WindowBound, error) {
	bound := plan.WindowBound{Kind: raw.Kind}
	if raw.Offset == nil {
		return bound, nil
	}
	old := b.clause.aggregateClause
	aggregates, windows := b.grouping.aggregates, b.grouping.windowCalls
	b.clause.aggregateClause = "window RANGE"
	defer func() {
		b.clause.aggregateClause = old
		b.grouping.aggregates = aggregates
		b.grouping.windowCalls = windows
	}()
	if unit == parser.FrameGroups || unit == parser.FrameRows {
		label := "GROUPS"
		if unit == parser.FrameRows {
			label = "ROWS"
		}
		b.clause.aggregateClause = "window " + label
		var err error
		bound.Offset, err = b.bindQueryBound(raw.Offset, label, analyze.ViewWindowLiteral, false)
		return bound, err
	}
	operand, err := b.comparisonOperand(raw.Offset)
	if err != nil {
		return bound, err
	}
	support, usage, err := sqltype.BindRange(order[0].Expression.ResultUsage(), operand.descriptor(), b.typ, b.diagnosticTypeName)
	if err != nil {
		if d, ok := err.(*sqlstate.Error); ok {
			copy := *d
			copy.Position = fmt.Sprint(raw.Offset.SourcePosition().Character)
			return bound, &copy
		}
		return bound, err
	}
	node, err := b.finalizeOperand(operand, usage.Type)
	if err != nil {
		return bound, err
	}
	if node.ResultUsage() != usage {
		coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), usage, sqltype.Implicit)
		if err != nil {
			return bound, err
		}
		node = plan.CoercionExpr{Info: b.info(usage, node.SourcePosition(), analyze.CoercionProperties(node, coercionPlan)), Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}
	}
	if analyze.ExpressionUsesFrame(node, b.frameID()) {
		return bound, expr.ErrorAt("42P10", "argument of RANGE must not contain variables", analyze.FirstQueryBoundFrameVariable(node, b.frameID()))
	}
	bound.Offset, bound.Support = node, support
	return bound, nil
}
