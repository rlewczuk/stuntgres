package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func rawNull(raw parser.Expr) bool {
	literal, ok := raw.(parser.LiteralExpr)
	return ok && literal.Value.Kind == parser.NullValue
}

func unknownOperand(raw parser.Expr, node expr.Expr) bool {
	if node.ResultUsage().Type == nil {
		return true
	}
	literal, ok := raw.(parser.LiteralExpr)
	return ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue)
}

// Only the coerced occurrence acquires a type. Earlier unknown parameters
// beneath null tests deliberately remain unresolved through final validation.
func (b *expressionBinder) contextualOperand(raw parser.Expr, node expr.Expr, u sqltype.Usage, context expressionContext) (expr.Expr, error) {
	if p, ok := node.(plan.ParameterExpr); ok {
		return b.CoerceParameter(p, u)
	}
	return b.Bind(raw, expressionPolicy{Context: context, Expected: u, operand: true})
}

func (b *expressionBinder) nullTest(raw parser.Expr, pos parser.Position, not, whole bool, context expressionContext) (expr.Expr, error) {
	child, err := b.Bind(raw, expressionPolicy{Context: context, operand: true})
	if err != nil {
		return nil, err
	}
	if p, ok := child.(plan.ParameterExpr); ok && p.Usage.Type == nil {
		b.Params.AnyParameters = append(b.Params.AnyParameters, p)
	}
	return plan.NullTestExpr{Info: b.Info(b.Type(sqltype.Bool).Usage(), pos, child.Properties()), Child: child, Not: not, Whole: whole}, nil
}

func (b *expressionBinder) bindPredicate(raw parser.PredicateExpr, context expressionContext) (expr.Expr, error) {
	switch raw.Kind {
	case parser.IsNull, parser.IsNotNull:
		return b.nullTest(raw.Left, raw.Pos, raw.Kind == parser.IsNotNull, false, context)
	case parser.IsTrue, parser.IsNotTrue, parser.IsFalse, parser.IsNotFalse, parser.IsUnknown, parser.IsNotUnknown:
		child, err := b.Bind(raw.Left, expressionPolicy{Context: context, operand: true})
		if err != nil {
			return nil, err
		}
		boolUsage := b.Type(sqltype.Bool).Usage()
		if unknownOperand(raw.Left, child) {
			child, err = b.contextualOperand(raw.Left, child, boolUsage, context)
			if err != nil {
				return nil, err
			}
		}
		names := [...]string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"}
		index := int(raw.Kind - parser.IsTrue)
		if child.ResultUsage().Type != boolUsage.Type {
			coercionPlan, err := sqltype.PlanCoercion(child.ResultUsage(), boolUsage, sqltype.Implicit)
			if err != nil {
				return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must be type boolean, not type %s", names[index], b.DiagnosticTypeName(child.ResultUsage().Type)), parser.ExpressionStart(raw.Left))
			}
			info, err := b.coercionInfo(b.Info(boolUsage, child.SourcePosition(), analyze.CoercionProperties(child, coercionPlan)), child)
			if err != nil {
				return nil, err
			}
			child = plan.CoercionExpr{Info: info, Child: child, Plan: coercionPlan, Mode: sqltype.Implicit}
		}
		truth := [...]plan.Truth{plan.True, plan.True, plan.False, plan.False, plan.Unknown, plan.Unknown}
		return plan.TruthTestExpr{Info: b.Info(boolUsage, raw.Pos, child.Properties()), Child: child, Truth: truth[index], Not: index%2 == 1}, nil
	case parser.IsDistinct, parser.IsNotDistinct:
		if left, ok := raw.Left.(parser.RowExpr); ok {
			if right, ok := raw.Right.(parser.RowExpr); ok {
				mode := "IS DISTINCT FROM"
				if raw.Kind == parser.IsNotDistinct {
					mode = "IS NOT DISTINCT FROM"
				}
				operator := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Pos: raw.Pos}
				return b.BindRowPredicate(operator, sqltype.CompareEqual, left, right, mode)
			}
		}
		if rawNull(raw.Left) {
			return b.nullTest(raw.Right, raw.Pos, raw.Kind == parser.IsDistinct, true, context)
		}
		if rawNull(raw.Right) {
			return b.nullTest(raw.Left, raw.Pos, raw.Kind == parser.IsDistinct, true, context)
		}
		// Use the ordinary operator-specific equality binder, preserving its
		// selected formal conversions, occurrence inference and dependencies.
		left, err := b.comparisonOperandPolicy(raw.Left, expressionPolicy{Context: context, operand: true})
		if err != nil {
			return nil, err
		}
		right, err := b.comparisonOperandPolicy(raw.Right, expressionPolicy{Context: context, operand: true})
		if err != nil {
			return nil, err
		}
		operator := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Args: []parser.Expr{raw.Left, raw.Right}, Pos: raw.Pos}
		bound, err := b.BindComparisonOperands(operator, sqltype.CompareEqual, left, right)
		if err != nil {
			return nil, err
		}
		equality := bound.(plan.ComparisonExpr)
		return plan.DistinctExpr{Info: equality.Info, Left: equality.Left, Right: equality.Right, Equality: equality.Predicate, Not: raw.Kind == parser.IsNotDistinct}, nil
	default:
		return nil, fmt.Errorf("stuntgres: invalid predicate kind")
	}
}
