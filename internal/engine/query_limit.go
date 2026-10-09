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
	if expr.Properties().RowDependent || expr.Properties().StageDependent {
		pos := firstQueryBoundVariable(expr)
		return nil, expressionError("42P10", "argument of "+clause+" must not contain variables", pos)
	}
	if b.noParameters && captureLiterals {
		if err := b.captureViewLiterals(raw, expr, literalClause, 0); err != nil {
			return nil, err
		}
	}
	return expr, nil
}

func firstQueryBoundVariable(expr BoundExpr) parser.Position {
	if expr == nil {
		return parser.Position{}
	}
	switch e := expr.(type) {
	case columnExpression, groupExpression, aggregateExpression:
		return expr.SourcePosition()
	case indexPropertyExpression:
		return firstQueryBoundArgs(e.children())
	case deparseExpression:
		return firstQueryBoundArgs(e.args)
	case visibilityInquiryExpression:
		return firstQueryBoundVariable(e.arg)
	case currentSchemasExpression:
		return firstQueryBoundVariable(e.arg)
	case collationExpression:
		return firstQueryBoundVariable(e.child)
	case coercionExpression:
		return firstQueryBoundVariable(e.child)
	case nullTestExpression:
		return firstQueryBoundVariable(e.child)
	case truthTestExpression:
		return firstQueryBoundVariable(e.child)
	case timeZoneExpression:
		if e.value.Properties().RowDependent {
			return firstQueryBoundVariable(e.value)
		}
		return firstQueryBoundVariable(e.zone)
	case bitwiseExpression:
		return firstQueryBoundPair(e.left, e.right)
	case arithmeticExpression:
		return firstQueryBoundPair(e.left, e.right)
	case likeExpression:
		return firstQueryBoundPair(e.left, e.right)
	case likeEscapeExpression:
		return firstQueryBoundPair(e.pattern, e.escape)
	case comparisonExpression:
		return firstQueryBoundPair(e.left, e.right)
	case logicalExpression:
		return firstQueryBoundPair(e.left, e.right)
	case distinctExpression:
		return firstQueryBoundPair(e.left, e.right)
	case caseExpression:
		if e.test != nil && e.test.Properties().RowDependent {
			return firstQueryBoundVariable(e.test)
		}
		for _, arm := range e.arms {
			if arm.condition.Properties().RowDependent {
				return firstQueryBoundVariable(arm.condition)
			}
			if arm.result.Properties().RowDependent {
				return firstQueryBoundVariable(arm.result)
			}
		}
		return firstQueryBoundVariable(e.otherwise)
	case conditionalExpression:
		return firstQueryBoundArgs(e.args)
	case sequenceCallExpression:
		return firstQueryBoundArgs(e.args)
	case binaryExpression:
		return firstQueryBoundArgs(e.args)
	case stringExpression:
		return firstQueryBoundArgs(e.args)
	case mathExpression:
		return firstQueryBoundArgs(e.args)
	case randomExpression:
		return firstQueryBoundArgs(e.args)
	case nullCountExpression:
		return firstQueryBoundArgs(e.args)
	case formatExpression:
		return firstQueryBoundArgs(e.args)
	}
	return expr.SourcePosition()
}
func firstQueryBoundPair(left, right BoundExpr) parser.Position {
	if left != nil && (left.Properties().RowDependent || left.Properties().StageDependent) {
		return firstQueryBoundVariable(left)
	}
	return firstQueryBoundVariable(right)
}
func firstQueryBoundArgs(args []BoundExpr) parser.Position {
	for _, arg := range args {
		if arg.Properties().RowDependent || arg.Properties().StageDependent {
			return firstQueryBoundVariable(arg)
		}
	}
	return parser.Position{}
}

func queryBoundValue(ctx context.Context, state *queryState, expr BoundExpr, clause string) (int64, bool, error) {
	if expr == nil {
		return 0, false, nil
	}
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: state.parameters, statementScope: state.source.scope, Input: state.input, Output: state.output}
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
