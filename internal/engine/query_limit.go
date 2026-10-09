package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type limitNode struct {
	querySchema
	child         LogicalNode
	offset, count BoundExpr
}

func (b *expressionBinder) bindQueryBounds(raw *parser.QueryBounds, captureLiterals bool) (*limitNode, error) {
	if raw == nil {
		return nil, nil
	}
	n := &limitNode{}
	var err error
	old := b.aggregateClause
	defer func() { b.aggregateClause = old }()
	b.aggregateClause = "OFFSET"
	n.offset, err = b.bindQueryBound(raw.Offset, "OFFSET", viewOffsetLiteral, captureLiterals)
	if err != nil {
		return nil, err
	}
	b.aggregateClause = "LIMIT"
	n.count, err = b.bindQueryBound(raw.Count, "LIMIT", viewLimitLiteral, captureLiterals)
	return n, err
}

func (b *expressionBinder) bindQueryBound(raw parser.Expr, clause string, literalClause viewLiteralClause, captureLiterals bool) (BoundExpr, error) {
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
		if b.parameters[r.Number].Type == nil {
			policy.expected = b.typ(sqltype.Int8).Usage()
		}
	}
	expr, err := b.bind(raw, policy)
	if err != nil {
		return nil, err
	}
	usage := b.typ(sqltype.Int8).Usage()
	if expr.ResultUsage() != usage {
		plan, err := sqltype.PlanCoercion(expr.ResultUsage(), usage, sqltype.Assignment)
		if err != nil {
			return nil, expressionError("42804", fmt.Sprintf("argument of %s must be type bigint, not type %s", clause, expr.ResultUsage().Type.DiagnosticName()), expr.SourcePosition())
		}
		expr = coercionExpression{b.info(usage, expr.SourcePosition(), coercionProperties(expr, plan)), expr, plan, sqltype.Assignment}
	}
	if expressionUsesFrame(expr, b.frameID()) {
		pos := firstQueryBoundFrameVariable(expr, b.frameID())
		return nil, expressionError("42P10", "argument of "+clause+" must not contain variables", pos)
	}
	if b.noParameters && captureLiterals {
		if err := b.captureViewLiterals(raw, expr, literalClause, 0); err != nil {
			return nil, err
		}
	}
	return expr, nil
}

func firstQueryBoundFrameVariable(expr BoundExpr, frame uint32) parser.Position {
	var position parser.Position
	found := false
	var query func(LogicalNode)
	var expression func(BoundExpr)
	expression = func(expr BoundExpr) {
		walkBoundExpression(expr, func(expr BoundExpr) {
			matched := false
			switch e := expr.(type) {
			case columnExpression:
				matched = e.frame == frame
			case groupExpression:
				matched = e.frame == frame
			case aggregateExpression:
				matched = e.frame == frame
			case subqueryExpression:
				query(e.queryPlan.Root)
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
	expression(expr)
	return position
}

func queryBoundValue(ctx context.Context, state *queryState, expr BoundExpr, clause string) (int64, bool, error) {
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
