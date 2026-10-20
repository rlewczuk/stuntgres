package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindQueryBounds(raw *parser.QueryBounds, captureLiterals bool) (*limitNode, error) {
	if raw == nil {
		return nil, nil
	}
	n := &limitNode{WithTies: raw.WithTies}
	var err error
	old := b.clause.aggregateClause
	defer func() { b.clause.aggregateClause = old }()
	b.clause.aggregateClause = "OFFSET"
	n.Offset, err = b.bindQueryBound(raw.Offset, "OFFSET", viewOffsetLiteral, captureLiterals)
	if err != nil {
		return nil, err
	}
	b.clause.aggregateClause = "LIMIT"
	n.Count, err = b.bindQueryBound(raw.Count, "LIMIT", viewLimitLiteral, captureLiterals)
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
		plan, err := sqltype.PlanCoercion(bound.ResultUsage(), usage, sqltype.Assignment)
		if err != nil {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must be type bigint, not type %s", clause, bound.ResultUsage().Type.DiagnosticName()), bound.SourcePosition())
		}
		bound = coercionExpression{b.info(usage, bound.SourcePosition(), coercionProperties(bound, plan)), bound, plan, sqltype.Assignment, false}
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
	var query func(LogicalNode)
	var expression func(expr.Expr)
	expression = func(bound expr.Expr) {
		walkBoundExpression(bound, func(expr expr.Expr) {
			matched := false
			switch e := expr.(type) {
			case columnExpression:
				matched = e.Frame == frame
			case returningWholeRowExpression:
				matched = e.Frame == frame
			case mergeActionExpression:
				matched = e.Frame == frame
			case groupExpression:
				matched = e.Frame == frame
			case aggregateExpression:
				matched = e.Frame == frame
			case groupingExpression:
				matched = e.Frame == frame
			case *windowExpression:
				matched = e.Frame == frame
			case subqueryExpression:
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
	query = func(node LogicalNode) {
		if node == nil {
			return
		}
		queryExpressions(node, expression)
		left, right := queryInputs(node)
		query(left)
		query(right)
	}
	expression(bound)
	return position
}

func queryBoundValue(ctx context.Context, state *queryState, expr expr.Expr, clause string) (int64, bool, error) {
	if expr == nil {
		return 0, false, nil
	}
	evaluation := state.evaluation(ctx)
	value, err := expr.Eval(&evaluation)
	if err != nil {
		return 0, false, err
	}
	if value.IsNull() {
		return 0, false, nil
	}
	n, err := value.Integer()
	if err != nil {
		return 0, false, err
	}
	if n < 0 {
		code := "2201W"
		if clause == "OFFSET" {
			code = "2201X"
		}
		return 0, false, sqlError(code, clause+" must not be negative")
	}
	return n, true, ctx.Err()
}
