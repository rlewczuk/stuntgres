package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type nullTestExpression struct {
	expr.Info
	child expr.Expr
	not   bool
	// DISTINCT FROM an undecorated NULL tests the whole value, even for rows.
	whole bool
}

func (e nullTestExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := e.child.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	result := v.IsNull()
	if !e.whole && !v.IsNull() && v.Type().BaseType().IsComposite() {
		result = true
		for i := range v.CompositeLen() {
			null := v.CompositeField(i).IsNull()
			if null == e.not {
				result = false
				break
			}
		}
	} else if e.not {
		result = !result
	}
	return sqltype.Boolean(e.Usage.Type, result), nil
}

type truthTestExpression struct {
	expr.Info
	child expr.Expr
	truth Truth
	not   bool
}

func (e truthTestExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := e.child.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	truth, err := TruthOf(v)
	if err != nil {
		return sqltype.Value{}, err
	}
	result := truth == e.truth
	if e.not {
		result = !result
	}
	return sqltype.Boolean(e.Usage.Type, result), nil
}

type distinctExpression struct {
	expr.Info
	left, right expr.Expr
	equality    sqltype.ComparisonPredicate
	not         bool
}

func (e distinctExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	distinct := a.IsNull() != b.IsNull()
	if !a.IsNull() && !b.IsNull() {
		equal, err := e.equality.Evaluate(ctx.FunctionContext(), a, b, ctx.FunctionInput())
		if err != nil {
			return sqltype.Value{}, err
		}
		truth, err := TruthOf(equal)
		if err != nil {
			return sqltype.Value{}, err
		}
		distinct = truth != True
	}
	if e.not {
		distinct = !distinct
	}
	return sqltype.Boolean(e.Usage.Type, distinct), nil
}

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
	if p, ok := node.(parameterExpression); ok {
		return b.coerceParameter(p, u)
	}
	return b.bind(raw, expressionPolicy{context: context, expected: u, operand: true})
}

func (b *expressionBinder) nullTest(raw parser.Expr, pos parser.Position, not, whole bool, context expressionContext) (expr.Expr, error) {
	child, err := b.bind(raw, expressionPolicy{context: context, operand: true})
	if err != nil {
		return nil, err
	}
	if p, ok := child.(parameterExpression); ok && p.Usage.Type == nil {
		b.params.anyParameters = append(b.params.anyParameters, p)
	}
	return nullTestExpression{b.info(b.typ(sqltype.Bool).Usage(), pos, child.Properties()), child, not, whole}, nil
}

func (b *expressionBinder) bindPredicate(raw parser.PredicateExpr, context expressionContext) (expr.Expr, error) {
	switch raw.Kind {
	case parser.IsNull, parser.IsNotNull:
		return b.nullTest(raw.Left, raw.Pos, raw.Kind == parser.IsNotNull, false, context)
	case parser.IsTrue, parser.IsNotTrue, parser.IsFalse, parser.IsNotFalse, parser.IsUnknown, parser.IsNotUnknown:
		child, err := b.bind(raw.Left, expressionPolicy{context: context, operand: true})
		if err != nil {
			return nil, err
		}
		boolUsage := b.typ(sqltype.Bool).Usage()
		if unknownOperand(raw.Left, child) {
			child, err = b.contextualOperand(raw.Left, child, boolUsage, context)
			if err != nil {
				return nil, err
			}
		}
		names := [...]string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"}
		index := int(raw.Kind - parser.IsTrue)
		if child.ResultUsage().Type != boolUsage.Type {
			plan, err := sqltype.PlanCoercion(child.ResultUsage(), boolUsage, sqltype.Implicit)
			if err != nil {
				return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must be type boolean, not type %s", names[index], b.diagnosticTypeName(child.ResultUsage().Type)), parser.ExpressionStart(raw.Left))
			}
			info, err := b.coercionInfo(b.info(boolUsage, child.SourcePosition(), coercionProperties(child, plan)), child)
			if err != nil {
				return nil, err
			}
			child = coercionExpression{info, child, plan, sqltype.Implicit, false}
		}
		truth := [...]Truth{True, True, False, False, Unknown, Unknown}
		return truthTestExpression{b.info(boolUsage, raw.Pos, child.Properties()), child, truth[index], index%2 == 1}, nil
	case parser.IsDistinct, parser.IsNotDistinct:
		if left, ok := raw.Left.(parser.RowExpr); ok {
			if right, ok := raw.Right.(parser.RowExpr); ok {
				mode := "IS DISTINCT FROM"
				if raw.Kind == parser.IsNotDistinct {
					mode = "IS NOT DISTINCT FROM"
				}
				operator := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Pos: raw.Pos}
				return b.bindRowPredicate(operator, sqltype.CompareEqual, left, right, mode)
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
		left, err := b.comparisonOperandPolicy(raw.Left, expressionPolicy{context: context, operand: true})
		if err != nil {
			return nil, err
		}
		right, err := b.comparisonOperandPolicy(raw.Right, expressionPolicy{context: context, operand: true})
		if err != nil {
			return nil, err
		}
		operator := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Args: []parser.Expr{raw.Left, raw.Right}, Pos: raw.Pos}
		bound, err := b.bindComparisonOperands(operator, sqltype.CompareEqual, left, right)
		if err != nil {
			return nil, err
		}
		equality := bound.(comparisonExpression)
		return distinctExpression{equality.Info, equality.left, equality.right, equality.predicate, raw.Kind == parser.IsNotDistinct}, nil
	default:
		return nil, fmt.Errorf("stuntgres: invalid predicate kind")
	}
}
