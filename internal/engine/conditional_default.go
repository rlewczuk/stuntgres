package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (r scalarRenderer) conditionalSQL(ctx context.Context, view CatalogRead, node BoundExpr) (string, error) {
	operand := func(child BoundExpr) (string, error) {
		text, err := r.sql(ctx, view, child)
		if err != nil {
			return "", err
		}
		// Retain known text and bigint leaves on replay; bare strings are unknown
		// and small bigint constants would otherwise turn into integer constants.
		if literal, ok := child.(literalExpression); ok && (literal.value.Type().ID() == sqltype.Text || literal.value.Type().ID() == sqltype.Int8 || literal.value.IsNull()) {
			if sqltype.BaselineCastType(child.ResultUsage().Type.ID()) {
				declaration, err := child.ResultUsage().Declaration()
				if err != nil {
					return "", err
				}
				return "CAST(" + text + " AS " + declaration + ")", nil
			}
		}
		return text, nil
	}
	switch e := node.(type) {
	case caseExpression:
		if len(e.arms) == 0 || e.otherwise == nil {
			return "", fmt.Errorf("stuntgres: invalid stored CASE")
		}
		if e.otherwise.ResultUsage().Type != e.usage.Type {
			return "", fmt.Errorf("stuntgres: invalid stored CASE result")
		}
		var sql strings.Builder
		sql.WriteString("(CASE")
		if e.test != nil {
			text, err := operand(e.test)
			if err != nil {
				return "", err
			}
			sql.WriteByte(' ')
			sql.WriteString(text)
		}
		for _, arm := range e.arms {
			if arm.result == nil || arm.result.ResultUsage().Type != e.usage.Type || arm.condition == nil {
				return "", fmt.Errorf("stuntgres: invalid stored CASE arm")
			}
			if e.test == nil {
				if arm.condition.ResultUsage().Type.ID() != sqltype.Bool {
					return "", fmt.Errorf("stuntgres: invalid stored CASE condition")
				}
			} else {
				plan, err := sqltype.PlanCoercion(e.test.ResultUsage(), arm.testUsage, sqltype.Implicit)
				if err != nil || plan != arm.testPlan {
					return "", fmt.Errorf("stuntgres: invalid stored CASE test coercion")
				}
				ref, conflict := arm.equality.InputCollation()
				comparison, err := sqltype.BindComparison(arm.testUsage.Type, arm.condition.ResultUsage().Type, ref, conflict)
				if err != nil || comparison != arm.equality {
					return "", fmt.Errorf("stuntgres: invalid stored CASE equality")
				}
			}
			condition, err := operand(arm.condition)
			if err != nil {
				return "", err
			}
			result, err := operand(arm.result)
			if err != nil {
				return "", err
			}
			sql.WriteString(" WHEN ")
			sql.WriteString(condition)
			sql.WriteString(" THEN ")
			sql.WriteString(result)
		}
		otherwise, err := operand(e.otherwise)
		if err != nil {
			return "", err
		}
		sql.WriteString(" ELSE ")
		sql.WriteString(otherwise)
		sql.WriteString(" END)")
		return sql.String(), nil
	case conditionalExpression:
		if len(e.args) == 0 || e.kind == parser.NullIf && len(e.args) != 2 || e.kind.String() == "" {
			return "", fmt.Errorf("stuntgres: invalid stored conditional")
		}
		if e.kind == parser.NullIf {
			if e.args[0].ResultUsage() != e.usage {
				return "", fmt.Errorf("stuntgres: invalid stored NULLIF result")
			}
		} else {
			for _, arg := range e.args {
				if arg == nil || arg.ResultUsage().Type != e.usage.Type {
					return "", fmt.Errorf("stuntgres: invalid stored conditional argument")
				}
			}
		}
		if e.kind != parser.Coalesce {
			left, right := e.usage.Type, e.usage.Type
			if e.kind == parser.NullIf {
				left, right = e.args[0].ResultUsage().Type, e.args[1].ResultUsage().Type
			}
			var comparison sqltype.Comparison
			var err error
			missingOrdering := false
			ref, conflict := e.comparison.InputCollation()
			if e.kind == parser.Greatest || e.kind == parser.Least {
				comparison, missingOrdering, err = bindExtremaComparison(e.usage, conflict)
			} else {
				comparison, err = sqltype.BindComparison(left, right, ref, conflict)
			}
			if err != nil || comparison != e.comparison || missingOrdering != e.missingOrdering {
				return "", fmt.Errorf("stuntgres: invalid stored conditional comparison")
			}
		}
		var sql strings.Builder
		sql.WriteString(e.kind.String())
		sql.WriteByte('(')
		for i, arg := range e.args {
			if i > 0 {
				sql.WriteByte(',')
			}
			text, err := operand(arg)
			if err != nil {
				return "", err
			}
			sql.WriteString(text)
		}
		sql.WriteByte(')')
		return sql.String(), nil
	}
	return "", fmt.Errorf("stuntgres: invalid stored conditional node")
}
