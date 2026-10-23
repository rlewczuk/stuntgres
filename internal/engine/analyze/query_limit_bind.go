package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindQueryBounds(raw *parser.QueryBounds, captureLiterals bool) (*plan.LimitNode, error) {
	if raw == nil {
		return nil, nil
	}
	n := &plan.LimitNode{WithTies: raw.WithTies}
	var err error
	old := b.Clause.AggregateClause
	defer func() { b.Clause.AggregateClause = old }()
	b.Clause.AggregateClause = "OFFSET"
	n.Offset, err = b.bindQueryBound(raw.Offset, "OFFSET", ViewOffsetLiteral, captureLiterals)
	if err != nil {
		return nil, err
	}
	b.Clause.AggregateClause = "LIMIT"
	n.Count, err = b.bindQueryBound(raw.Count, "LIMIT", ViewLimitLiteral, captureLiterals)
	if err == nil && raw.WithTies {
		if literal, ok := raw.Count.(parser.LiteralExpr); ok && literal.Value.Kind == parser.NullValue {
			return nil, sqlError("2201W", "row count cannot be null in FETCH FIRST ... WITH TIES clause")
		}
	}
	return n, err
}

func (b *Binder) bindQueryBound(raw parser.Expr, clause string, literalClause ViewLiteralClause, captureLiterals bool) (expr.Expr, error) {
	if raw == nil {
		return nil, nil
	}
	policy := ExpressionPolicy{Context: ProjectionContext}
	switch r := raw.(type) {
	case parser.LiteralExpr:
		if r.Value.Kind == parser.NullValue || r.Value.Kind == parser.StringValue {
			policy.Expected = b.Type(sqltype.Int8).Usage()
		}
	case parser.ParameterExpr:
		if b.Params.Parameters[r.Number].Type == nil {
			policy.Expected = b.Type(sqltype.Int8).Usage()
		}
	}
	bound, err := b.Bind(raw, policy)
	if err != nil {
		return nil, err
	}
	usage := b.Type(sqltype.Int8).Usage()
	if selected, handled, selectionErr := b.declaredCoercion(bound, usage, sqltype.Assignment, bound.SourcePosition(), raw); handled {
		if selectionErr != nil {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must be type bigint, not type %s", clause, bound.ResultUsage().Type.DiagnosticName()), bound.SourcePosition())
		}
		bound = selected
	}
	if bound.ResultUsage() != usage {
		coercionPlan, err := sqltype.PlanCoercion(bound.ResultUsage(), usage, sqltype.Assignment)
		if err != nil {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must be type bigint, not type %s", clause, bound.ResultUsage().Type.DiagnosticName()), bound.SourcePosition())
		}
		bound = plan.CoercionExpr{Info: b.Info(usage, bound.SourcePosition(), CoercionProperties(bound, coercionPlan)), Child: bound, Plan: coercionPlan, Mode: sqltype.Assignment}
	}
	if ExpressionUsesFrame(bound, b.FrameID()) {
		pos := FirstQueryBoundFrameVariable(bound, b.FrameID())
		return nil, expr.ErrorAt("42P10", "argument of "+clause+" must not contain variables", pos)
	}
	if (b.Params.NoParameters || b.Consumer.frozenDefinition) && captureLiterals {
		if err := b.CaptureViewLiterals(raw, bound, literalClause, 0); err != nil {
			return nil, err
		}
	}
	return bound, nil
}
