package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type nullTestExpression struct {
	expressionInfo
	child BoundExpr
	not   bool
}

func (e nullTestExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	v, err := e.child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	result := v.IsNull()
	if e.not {
		result = !result
	}
	return sqltype.Boolean(e.usage.Type, result), nil
}

type truthTestExpression struct {
	expressionInfo
	child BoundExpr
	truth Truth
	not   bool
}

func (e truthTestExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	v, err := e.child.Eval(c)
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
	return sqltype.Boolean(e.usage.Type, result), nil
}

type distinctExpression struct {
	expressionInfo
	left, right BoundExpr
	equality    sqltype.ComparisonPredicate
	not         bool
}

func (e distinctExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	distinct := a.IsNull() != b.IsNull()
	if !a.IsNull() && !b.IsNull() {
		equal, err := e.equality.Evaluate(c.Context, a, b)
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
	return sqltype.Boolean(e.usage.Type, distinct), nil
}

func rawNull(raw parser.Expr) bool {
	literal, ok := raw.(parser.LiteralExpr)
	return ok && literal.Value.Kind == parser.NullValue
}

func unknownOperand(raw parser.Expr, node BoundExpr) bool {
	if node.ResultUsage().Type == nil {
		return true
	}
	literal, ok := raw.(parser.LiteralExpr)
	return ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue)
}

// Only the coerced occurrence acquires a type. Earlier unknown parameters
// beneath null tests deliberately remain unresolved through final validation.
func (b *expressionBinder) contextualOperand(raw parser.Expr, node BoundExpr, u sqltype.Usage, context expressionContext) (BoundExpr, error) {
	if p, ok := node.(parameterExpression); ok {
		return b.coerceParameter(p, u)
	}
	return b.bind(raw, expressionPolicy{context: context, expected: u, operand: true})
}

func (b *expressionBinder) nullTest(raw parser.Expr, pos parser.Position, not bool, context expressionContext) (BoundExpr, error) {
	child, err := b.bind(raw, expressionPolicy{context: context, operand: true})
	if err != nil {
		return nil, err
	}
	if p, ok := child.(parameterExpression); ok && p.usage.Type == nil {
		b.anyParameters = append(b.anyParameters, p)
	}
	return nullTestExpression{b.info(b.typ(sqltype.Bool).Usage(), pos, child.Properties()), child, not}, nil
}

func (b *expressionBinder) bindPredicate(raw parser.PredicateExpr, context expressionContext) (BoundExpr, error) {
	switch raw.Kind {
	case parser.IsNull, parser.IsNotNull:
		return b.nullTest(raw.Left, raw.Pos, raw.Kind == parser.IsNotNull, context)
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
			return nil, expressionError("42804", fmt.Sprintf("argument of %s must be type boolean, not type %s", names[index], child.ResultUsage().Type.DiagnosticName()), raw.Left.SourcePosition())
		}
		truth := [...]Truth{True, True, False, False, Unknown, Unknown}
		return truthTestExpression{b.info(boolUsage, raw.Pos, child.Properties()), child, truth[index], index%2 == 1}, nil
	case parser.IsDistinct, parser.IsNotDistinct:
		if rawNull(raw.Left) {
			return b.nullTest(raw.Right, raw.Pos, raw.Kind == parser.IsDistinct, context)
		}
		if rawNull(raw.Right) {
			return b.nullTest(raw.Left, raw.Pos, raw.Kind == parser.IsDistinct, context)
		}
		left, err := b.bind(raw.Left, expressionPolicy{context: context, operand: true})
		if err != nil {
			return nil, err
		}
		right, err := b.bind(raw.Right, expressionPolicy{context: context, operand: true})
		if err != nil {
			return nil, err
		}
		leftUnknown, rightUnknown := unknownOperand(raw.Left, left), unknownOperand(raw.Right, right)
		u := b.typ(sqltype.Text).Usage()
		if !leftUnknown {
			u = left.ResultUsage()
		} else if !rightUnknown {
			u = right.ResultUsage()
		}
		if leftUnknown {
			left, err = b.contextualOperand(raw.Left, left, u.Type.Usage(), context)
			if err != nil {
				return nil, err
			}
		}
		if rightUnknown {
			right, err = b.contextualOperand(raw.Right, right, u.Type.Usage(), context)
			if err != nil {
				return nil, err
			}
		}
		equality, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, left.ResultUsage().Type, right.ResultUsage().Type, b.typ(sqltype.Bool))
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && d.Code == "42883" {
				copy := *d
				copy.Message = fmt.Sprintf("operator does not exist: %s = %s", left.ResultUsage().Type.DiagnosticName(), right.ResultUsage().Type.DiagnosticName())
				copy.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				copy.Position = strconv.Itoa(raw.Pos.Character)
				return nil, &copy
			}
			return nil, err
		}
		p := left.Properties()
		p.RowDependent = p.RowDependent || right.Properties().RowDependent
		p.ParameterDependent = p.ParameterDependent || right.Properties().ParameterDependent
		p.StageDependent = p.StageDependent || right.Properties().StageDependent
		return distinctExpression{b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, p), left, right, equality, raw.Kind == parser.IsNotDistinct}, nil
	default:
		return nil, fmt.Errorf("stuntgres: invalid predicate kind")
	}
}
