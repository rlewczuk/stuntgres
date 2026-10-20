package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Boolean context resolves only unknown inputs. A known parameter's type is
// preserved just like a column's type, rather than assignment-coerced to BOOL.
func (b *expressionBinder) bindBoolean(raw parser.Expr, context expressionContext, construct string) (expr.Expr, error) {
	u := b.typ(sqltype.Bool).Usage()
	if p, ok := raw.(parser.ParameterExpr); ok {
		if known := b.params.parameters[p.Number]; known.Type != nil {
			u = known
		}
	}
	return b.bind(raw, expressionPolicy{context: context, expected: u, booleanConstruct: construct})
}

func (b *expressionBinder) bindLogical(raw parser.LogicalExpr, context expressionContext) (expr.Expr, error) {
	if context == assignmentContext || raw.Left == nil {
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
	left, err := b.bindBoolean(raw.Left, context, construct)
	if err != nil {
		return nil, err
	}
	var right expr.Expr
	properties := left.Properties()
	queryFrame := b.query.nextQueryFrame
	if raw.Right != nil {
		right, err = b.bindBoolean(raw.Right, context, construct)
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
	return logicalExpression{Info: b.info(b.typ(sqltype.Bool).Usage(), pos, properties), op: raw.Op, left: left, right: right, queryRight: b.query.nextQueryFrame != queryFrame}, nil
}
