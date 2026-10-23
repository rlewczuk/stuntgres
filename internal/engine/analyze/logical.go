package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Boolean context infers unknown inputs as BOOL, but binds known parameters in
// their original type before selecting an assignment conversion to BOOL.
func (b *Binder) BindBoolean(raw parser.Expr, context ExpressionContext, construct string) (expr.Expr, error) {
	u := b.Type(sqltype.Bool).Usage()
	if p, ok := raw.(parser.ParameterExpr); ok {
		if known := b.Params.Parameters[p.Number]; known.Type != nil {
			u = known
		}
	}
	return b.Bind(raw, ExpressionPolicy{Context: context, Expected: u, booleanConstruct: construct})
}

func (b *Binder) bindLogical(raw parser.LogicalExpr, context ExpressionContext) (expr.Expr, error) {
	if context == AssignmentContext || raw.Left == nil {
		return nil, fmt.Errorf("stuntgres: invalid logical expression")
	}
	var construct string
	switch raw.Op {
	case parser.LogicalAnd:
		construct = "AND"
	case parser.LogicalOr:
		construct = "OR"
	case parser.LogicalNot:
		construct = "NOT"
	default:
		return nil, fmt.Errorf("stuntgres: invalid logical operator")
	}
	if (raw.Op == parser.LogicalNot) != (raw.Right == nil) {
		return nil, fmt.Errorf("stuntgres: invalid logical arity")
	}
	left, err := b.BindBoolean(raw.Left, context, construct)
	if err != nil {
		return nil, err
	}
	var right expr.Expr
	properties := left.Properties()
	queryFrame := b.Query.NextQueryFrame
	if raw.Right != nil {
		right, err = b.BindBoolean(raw.Right, context, construct)
		if err != nil {
			return nil, err
		}
		p := right.Properties()
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
		if p.Volatility > properties.Volatility {
			properties.Volatility = p.Volatility
		}
	}
	// Children already retain their earliest positions. Combining those cached
	// positions avoids walking raw subtrees again for every parent in a chain.
	pos := raw.Pos
	for _, child := range [...]expr.Expr{left, right} {
		if child != nil {
			p := child.SourcePosition()
			if p.Character > 0 && (pos.Character <= 0 || p.Character < pos.Character) {
				pos = p
			}
		}
	}
	return plan.LogicalExpr{Info: b.Info(b.Type(sqltype.Bool).Usage(), pos, properties), Op: raw.Op, Left: left, Right: right, QueryRight: b.Query.NextQueryFrame != queryFrame}, nil
}
