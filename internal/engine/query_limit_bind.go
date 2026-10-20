package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindQueryBounds(raw *parser.QueryBounds, captureLiterals bool) (*plan.LimitNode, error) {
	if raw == nil {
		return nil, nil
	}
	n := &plan.LimitNode{WithTies: raw.WithTies}
	var err error
	old := b.clause.aggregateClause
	defer func() { b.clause.aggregateClause = old }()
	b.clause.aggregateClause = "OFFSET"
	n.Offset, err = b.bindQueryBound(raw.Offset, "OFFSET", analyze.ViewOffsetLiteral, captureLiterals)
	if err != nil {
		return nil, err
	}
	b.clause.aggregateClause = "LIMIT"
	n.Count, err = b.bindQueryBound(raw.Count, "LIMIT", analyze.ViewLimitLiteral, captureLiterals)
	if err == nil && raw.WithTies {
		if literal, ok := raw.Count.(parser.LiteralExpr); ok && literal.Value.Kind == parser.NullValue {
			return nil, sqlError("2201W", "row count cannot be null in FETCH FIRST ... WITH TIES clause")
		}
	}
	return n, err
}

func (b *expressionBinder) bindQueryBound(raw parser.Expr, clause string, literalClause viewLiteralClause, captureLiterals bool) (expr.Expr, error) {
	if raw == nil {
		return nil, nil
	}
	policy := expressionPolicy{context: projectionContext}
	switch r := raw.(type) {
	case parser.LiteralExpr:
		if r.Value.Kind == parser.NullValue || r.Value.Kind == parser.StringValue {
			policy.expected = b.typ(sqltype.Int8).Usage()
		}
	case parser.ParameterExpr:
		if b.params.parameters[r.Number].Type == nil {
			policy.expected = b.typ(sqltype.Int8).Usage()
		}
	}
	bound, err := b.bind(raw, policy)
	if err != nil {
		return nil, err
	}
	usage := b.typ(sqltype.Int8).Usage()
	if bound.ResultUsage() != usage {
		coercionPlan, err := sqltype.PlanCoercion(bound.ResultUsage(), usage, sqltype.Assignment)
		if err != nil {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must be type bigint, not type %s", clause, bound.ResultUsage().Type.DiagnosticName()), bound.SourcePosition())
		}
		bound = plan.CoercionExpr{Info: b.info(usage, bound.SourcePosition(), coercionProperties(bound, coercionPlan)), Child: bound, Plan: coercionPlan, Mode: sqltype.Assignment}
	}
	if expressionUsesFrame(bound, b.frameID()) {
		pos := firstQueryBoundFrameVariable(bound, b.frameID())
		return nil, expr.ErrorAt("42P10", "argument of "+clause+" must not contain variables", pos)
	}
	if (b.params.noParameters || b.consumer.materializedDefinition) && captureLiterals {
		if err := b.captureViewLiterals(raw, bound, literalClause, 0); err != nil {
			return nil, err
		}
	}
	return bound, nil
}

func firstQueryBoundFrameVariable(bound expr.Expr, frame uint32) parser.Position {
	var position parser.Position
	found := false
	var query func(plan.Node)
	var expression func(expr.Expr)
	expression = func(bound expr.Expr) {
		plan.WalkExpression(bound, func(expr expr.Expr) {
			matched := false
			switch e := expr.(type) {
			case plan.ColumnExpr:
				matched = e.Frame == frame
			case plan.ReturningWholeRowExpr:
				matched = e.Frame == frame
			case plan.MergeActionExpr:
				matched = e.Frame == frame
			case plan.GroupExpr:
				matched = e.Frame == frame
			case plan.AggregateExpr:
				matched = e.Frame == frame
			case plan.GroupingExpr:
				matched = e.Frame == frame
			case *plan.WindowExpr:
				matched = e.Frame == frame
			case plan.SubqueryExpr:
				query(e.QueryPlan.Root)
			}
			if matched {
				p := expr.SourcePosition()
				if !found || p.Character < position.Character {
					position = p
					found = true
				}
			}
		})
	}
	query = func(node plan.Node) {
		if node == nil {
			return
		}
		plan.QueryExpressions(node, expression)
		left, right := plan.QueryInputs(node)
		query(left)
		query(right)
	}
	expression(bound)
	return position
}
