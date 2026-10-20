package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (r scalarRenderer) conditionalSQL(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	operand := func(child expr.Expr) (string, error) {
		text, err := r.sql(ctx, view, child)
		if err != nil {
			return "", err
		}
		// Retain known text and bigint leaves on replay; bare strings are unknown
		// and small bigint constants would otherwise turn into integer constants.
		if literal, ok := child.(literalExpression); ok && (literal.Value.Type().ID() == sqltype.Text || literal.Value.Type().ID() == sqltype.Int8 || literal.Value.IsNull()) {
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
		if len(e.Arms) == 0 || e.Otherwise == nil {
			return "", fmt.Errorf("stuntgres: invalid stored CASE")
		}
		if e.Otherwise.ResultUsage().Type != e.Usage.Type {
			return "", fmt.Errorf("stuntgres: invalid stored CASE result")
		}
		var sql strings.Builder
		sql.WriteString("(CASE")
		if e.Test != nil {
			text, err := operand(e.Test)
			if err != nil {
				return "", err
			}
			sql.WriteByte(' ')
			sql.WriteString(text)
		}
		for _, arm := range e.Arms {
			if arm.Result == nil || arm.Result.ResultUsage().Type != e.Usage.Type || arm.Condition == nil {
				return "", fmt.Errorf("stuntgres: invalid stored CASE arm")
			}
			if e.Test == nil {
				if arm.Condition.ResultUsage().Type.ID() != sqltype.Bool {
					return "", fmt.Errorf("stuntgres: invalid stored CASE condition")
				}
			} else {
				plan, err := sqltype.PlanCoercion(e.Test.ResultUsage(), arm.TestUsage, sqltype.Implicit)
				if err != nil || plan != arm.TestPlan {
					return "", fmt.Errorf("stuntgres: invalid stored CASE test coercion")
				}
				ref, conflict := arm.Equality.InputCollation()
				comparison, err := sqltype.BindComparison(arm.TestUsage.Type, arm.Condition.ResultUsage().Type, ref, conflict)
				if err != nil || !comparison.Equal(arm.Equality) {
					return "", fmt.Errorf("stuntgres: invalid stored CASE equality")
				}
			}
			condition, err := operand(arm.Condition)
			if err != nil {
				return "", err
			}
			result, err := operand(arm.Result)
			if err != nil {
				return "", err
			}
			sql.WriteString(" WHEN ")
			sql.WriteString(condition)
			sql.WriteString(" THEN ")
			sql.WriteString(result)
		}
		otherwise, err := operand(e.Otherwise)
		if err != nil {
			return "", err
		}
		sql.WriteString(" ELSE ")
		sql.WriteString(otherwise)
		sql.WriteString(" END)")
		return sql.String(), nil
	case conditionalExpression:
		if len(e.Args) == 0 || e.Kind == parser.NullIf && len(e.Args) != 2 || e.Kind.String() == "" {
			return "", fmt.Errorf("stuntgres: invalid stored conditional")
		}
		if e.Kind == parser.NullIf {
			if e.Args[0].ResultUsage() != e.Usage {
				return "", fmt.Errorf("stuntgres: invalid stored NULLIF result")
			}
		} else {
			for _, arg := range e.Args {
				if arg == nil || arg.ResultUsage().Type != e.Usage.Type {
					return "", fmt.Errorf("stuntgres: invalid stored conditional argument")
				}
			}
		}
		if e.Kind != parser.Coalesce {
			left, right := e.Usage.Type, e.Usage.Type
			if e.Kind == parser.NullIf {
				left, right = e.Args[0].ResultUsage().Type, e.Args[1].ResultUsage().Type
			}
			var comparison sqltype.Comparison
			var err error
			missingOrdering := false
			ref, conflict := e.Comparison.InputCollation()
			if e.Kind == parser.Greatest || e.Kind == parser.Least {
				comparison, missingOrdering, err = bindExtremaComparison(e.Usage, conflict)
			} else {
				comparison, err = sqltype.BindComparison(left, right, ref, conflict)
			}
			if err != nil || !comparison.Equal(e.Comparison) || missingOrdering != e.MissingOrdering {
				return "", fmt.Errorf("stuntgres: invalid stored conditional comparison")
			}
		}
		var sql strings.Builder
		sql.WriteString(e.Kind.String())
		sql.WriteByte('(')
		for i, arg := range e.Args {
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
