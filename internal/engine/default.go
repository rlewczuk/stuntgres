package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Published definitions contain immutable typed nodes, never evaluated values
// or caller-owned syntax. Attribute keys survive descriptor replacements.
type columnDefault struct {
	expression BoundExpr
	sql        string
}

func (b *expressionBinder) bindDefault(raw parser.Expr, column ColumnDescriptor) (BoundExpr, error) {
	// Recursive binding carries this consumer policy through Boolean and
	// comparison children, preserving their ordinary diagnostic order.
	previous := b.defaultConsumer
	b.defaultConsumer = true
	defer func() { b.defaultConsumer = previous }()
	node, err := b.bind(raw, expressionPolicy{context: projectionContext, expected: column.Usage.Type.Usage()})
	if err != nil {
		return nil, err
	}
	if node.ResultUsage() != column.Usage {
		plan, err := sqltype.PlanCoercion(node.ResultUsage(), column.Usage, sqltype.Assignment)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42804" {
				d := sqlError("42804", fmt.Sprintf(`column "%s" is of type %s but default expression is of type %s`, column.Name, column.Usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())).(*sqlstate.Error)
				d.Hint = "You will need to rewrite or cast the expression."
				return nil, d
			}
			return nil, err
		}
		node = coercionExpression{b.info(column.Usage, parser.Position{}, node.Properties()), node, plan, sqltype.Assignment}
	}
	return node, nil
}

func bindCreateDefaults(b *expressionBinder, columns []ColumnDescriptor, declarations []createColumnDeclaration) (map[int32]columnDefault, error) {
	var defaults map[int32]columnDefault
	view := CatalogRead{owner: b.engine, state: b.engine.state}
	for slot := range declarations {
		raw := declarations[slot].defaultExpr
		if raw == nil {
			continue
		}
		node, err := b.bindDefault(raw, columns[slot])
		if err != nil {
			return nil, err
		}
		canonical, err := defaultSQL(b.ctx, view, node)
		if err != nil {
			return nil, err
		}
		if defaults == nil {
			defaults = make(map[int32]columnDefault)
		}
		defaults[int32(slot+1)] = columnDefault{node, canonical}
	}
	return defaults, nil
}

// Serialize only closed supported nodes, validating ownership and dependence
// without Eval. Assignment wrappers intentionally retain deferred conversion.
func defaultSQL(ctx context.Context, view CatalogRead, node BoundExpr) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil {
		return "", fmt.Errorf("stuntgres: missing default node")
	}
	u, p := node.ResultUsage(), node.Properties()
	if u.Type == nil || !u.Valid() || !u.Type.Concrete() || p != (ExprProperties{}) {
		return "", fmt.Errorf("stuntgres: invalid stored default properties")
	}
	typ, ok := view.LookupType(u.Type.ID())
	if !ok || typ != u.Type {
		return "", fmt.Errorf("stuntgres: foreign default type")
	}
	switch n := node.(type) {
	case literalExpression:
		if n.value.Type() != u.Type {
			return "", fmt.Errorf("stuntgres: invalid default literal")
		}
		literal, err := u.Type.SQLLiteral(ctx, n.value)
		if err != nil || n.value.IsNull() {
			return literal, err
		}
		switch u.Type.ID() {
		case sqltype.Date:
			return "DATE " + literal, nil
		case sqltype.Time:
			return "TIME " + literal, nil
		case sqltype.Timestamp:
			// The type-owned endpoint encoding already includes its required
			// precision-bearing introducer; never prepend a second one.
			if strings.HasPrefix(literal, "TIMESTAMP") {
				return literal, nil
			}
			return "TIMESTAMP " + literal, nil
		case sqltype.Numeric:
			text, err := u.Type.Output(ctx, n.value)
			if err != nil {
				return "", err
			}
			if text == "NaN" || text == "Infinity" || text == "-Infinity" {
				return literal, nil
			}
			if !strings.ContainsAny(text, ".eE") {
				text += ".0"
			}
			return text, nil
		}
		return literal, nil
	case coercionExpression:
		if n.child == nil {
			return "", fmt.Errorf("stuntgres: missing default coercion child")
		}
		switch n.mode {
		case sqltype.Implicit, sqltype.Assignment, sqltype.Explicit:
		default:
			return "", fmt.Errorf("stuntgres: invalid default coercion mode")
		}
		plan, err := sqltype.PlanCoercion(n.child.ResultUsage(), u, n.mode)
		if err != nil || plan != n.plan {
			return "", fmt.Errorf("stuntgres: invalid default coercion")
		}
		child, err := defaultSQL(ctx, view, n.child)
		if err != nil || n.mode != sqltype.Explicit {
			return child, err
		}
		// Bare quoted TEXT becomes unknown on replay and can move a deferred
		// conversion into CREATE-time input decoding. Keep that child typed.
		if n.child.ResultUsage().Type.ID() == sqltype.Text {
			inner, explicit := n.child.(coercionExpression)
			if !explicit || inner.mode != sqltype.Explicit {
				declaration, err := n.child.ResultUsage().Declaration()
				if err != nil {
					return "", err
				}
				child = "CAST(" + child + " AS " + declaration + ")"
			}
		}
		declaration, err := u.Declaration()
		if err != nil {
			return "", err
		}
		return "CAST(" + child + " AS " + declaration + ")", nil
	case comparisonExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || n.right == nil {
			return "", fmt.Errorf("stuntgres: invalid default predicate")
		}
		var operator sqltype.ComparisonOperator
		switch n.operator {
		case "=":
			operator = sqltype.CompareEqual
		case "<>":
			operator = sqltype.CompareNotEqual
		case "<":
			operator = sqltype.CompareLess
		case "<=":
			operator = sqltype.CompareLessEqual
		case ">":
			operator = sqltype.CompareGreater
		case ">=":
			operator = sqltype.CompareGreaterEqual
		default:
			return "", fmt.Errorf("stuntgres: invalid default comparison")
		}
		predicate, err := sqltype.BindComparisonPredicate(operator, n.left.ResultUsage().Type, n.right.ResultUsage().Type, u.Type)
		if err != nil || predicate != n.predicate {
			return "", fmt.Errorf("stuntgres: invalid default predicate binding")
		}
		left, err := defaultSQL(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		right, err := defaultSQL(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + n.operator + " " + right + ")", nil
	case nullTestExpression:
		if u.Type.ID() != sqltype.Bool || n.child == nil {
			return "", fmt.Errorf("stuntgres: invalid default null test")
		}
		child, err := defaultSQL(ctx, view, n.child)
		if err != nil {
			return "", err
		}
		operator := "IS NULL"
		if n.not {
			operator = "IS NOT NULL"
		}
		return "(" + child + " " + operator + ")", nil
	case truthTestExpression:
		if u.Type.ID() != sqltype.Bool || n.child == nil || n.child.ResultUsage().Type != u.Type {
			return "", fmt.Errorf("stuntgres: invalid default truth test")
		}
		var truth string
		switch n.truth {
		case True:
			truth = "TRUE"
		case False:
			truth = "FALSE"
		case Unknown:
			truth = "UNKNOWN"
		default:
			return "", fmt.Errorf("stuntgres: invalid default truth value")
		}
		child, err := defaultSQL(ctx, view, n.child)
		if err != nil {
			return "", err
		}
		operator := "IS"
		if n.not {
			operator = "IS NOT"
		}
		return "(" + child + " " + operator + " " + truth + ")", nil
	case distinctExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || n.right == nil {
			return "", fmt.Errorf("stuntgres: invalid default distinct test")
		}
		predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, n.left.ResultUsage().Type, n.right.ResultUsage().Type, u.Type)
		if err != nil || predicate != n.equality {
			return "", fmt.Errorf("stuntgres: invalid default distinct binding")
		}
		left, err := defaultSQL(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		right, err := defaultSQL(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		operator := "IS DISTINCT FROM"
		if n.not {
			operator = "IS NOT DISTINCT FROM"
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case logicalExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || n.left.ResultUsage().Type.ID() != sqltype.Bool || n.op == parser.LogicalNot && n.right != nil || n.op != parser.LogicalNot && (n.right == nil || n.right.ResultUsage().Type.ID() != sqltype.Bool) {
			return "", fmt.Errorf("stuntgres: invalid default logical binding")
		}
		left, err := defaultSQL(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		if n.op == parser.LogicalNot {
			return "(NOT " + left + ")", nil
		}
		right, err := defaultSQL(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		operator := "AND"
		if n.op == parser.LogicalOr {
			operator = "OR"
		} else if n.op != parser.LogicalAnd {
			return "", fmt.Errorf("stuntgres: invalid default logical operator")
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	default:
		return "", fmt.Errorf("stuntgres: unsupported stored default node")
	}
}
