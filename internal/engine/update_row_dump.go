package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Destination coercions rebind from the independent SELECT's output schema on
// Restore. Rendering each output as a scalar SELECT would duplicate its demand.
func ruleUpdateAssignmentsSQL(ctx context.Context, view catalog.Read, b *binding, action ruleAction) (string, error) {
	var sql strings.Builder
	targetSQL := func(i int) (string, error) {
		column, err := deparseAttribute(b.relation, b.relation.Attributes[b.targets[i]])
		if err != nil {
			return "", err
		}
		if len(b.update.arrayTargets) > i {
			return ruleAssignmentSQL(ctx, view, column, b.update.arrayTargets[i], action)
		}
		return column, nil
	}
	group := 0
	for i := 0; i < len(b.update.assignments); {
		if i != 0 {
			sql.WriteString(", ")
		}
		if group < len(b.update.groups) && b.update.groups[group].start == i {
			width := b.update.groups[group].width
			sql.WriteByte('(')
			for j := range width {
				if j != 0 {
					sql.WriteString(", ")
				}
				column, err := targetSQL(i + j)
				if err != nil {
					return "", err
				}
				sql.WriteString(column)
			}
			sql.WriteString(") = ")
			var source expr.Expr
			walkBoundExpression(b.update.assignments[i], func(node expr.Expr) {
				if n, ok := node.(plan.SubqueryExpr); ok && n.UpdateWidth != 0 {
					source = n
				}
			})
			if source == nil {
				return "", fmt.Errorf("stuntgres: missing grouped UPDATE source for replay")
			}
			text, err := ruleExpressionSQL(ctx, view, source, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(text)
			i += width
			group++
			continue
		}
		column, err := targetSQL(i)
		if err != nil {
			return "", err
		}
		text, err := ruleExpressionSQL(ctx, view, b.update.assignments[i], action)
		if err != nil {
			return "", err
		}
		sql.WriteString(column + " = " + text)
		i++
	}
	return sql.String(), nil
}
