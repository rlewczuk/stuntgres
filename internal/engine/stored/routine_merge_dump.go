package stored

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// mergeActionSQL renders checked MERGE recipes, not original relation names or
// argument syntax. WITH and RETURNING are shared with other mutation actions.
func mergeActionSQL(ctx context.Context, view catalog.Read, action ruleRendering) (string, error) {
	b := action.binding
	if b.Merge == nil || b.Relation == nil {
		return "", fmt.Errorf("stuntgres: missing checked MERGE recipe")
	}
	merge := b.Merge
	name, err := action.relation(view, b.Ref, false)
	if err != nil {
		return "", err
	}
	from, err := ruleFromSQL(ctx, view, merge.Source, action)
	if err != nil {
		return "", err
	}
	on, err := ruleExpressionSQL(ctx, view, merge.On, action)
	if err != nil {
		return "", err
	}
	var sql strings.Builder
	sql.WriteString("MERGE INTO " + name + " USING " + from + " ON " + on)
	for i, arm := range merge.Arms {
		sql.WriteString(" WHEN ")
		switch arm.Match {
		case parser.MergeMatched:
			sql.WriteString("MATCHED")
		case parser.MergeNotMatched:
			sql.WriteString("NOT MATCHED BY TARGET")
		case parser.MergeNotMatchedBySource:
			sql.WriteString("NOT MATCHED BY SOURCE")
		default:
			return "", fmt.Errorf("stuntgres: invalid MERGE match recipe")
		}
		render := func(value expr.Expr) (string, error) {
			if arm.Match == parser.MergeNotMatched {
				return ruleExpressionSQL(ctx, view, value, action, merge.Source)
			}
			return ruleExpressionSQL(ctx, view, value, action)
		}
		if arm.Condition != nil {
			condition, err := render(arm.Condition)
			if err != nil {
				return "", err
			}
			sql.WriteString(" AND " + condition)
		}
		sql.WriteString(" THEN ")
		switch arm.Action {
		case parser.MergeNothingAction:
			sql.WriteString("DO NOTHING")
		case parser.MergeDeleteAction:
			sql.WriteString("DELETE")
		case parser.MergeUpdateAction:
			if arm.Input.Update == nil {
				return "", fmt.Errorf("stuntgres: missing MERGE UPDATE recipe")
			}
			sql.WriteString("UPDATE SET ")
			for j, value := range arm.Input.Update.Assignments {
				if j != 0 {
					sql.WriteString(", ")
				}
				column, err := DeparseAttribute(view.MustRelation(b.Ref.Address), arm.Input.Relation.Attributes[arm.Input.Targets[j]])
				if err != nil {
					return "", err
				}
				if len(arm.Input.Update.ArrayTargets) > j {
					column, err = ruleAssignmentSQL(ctx, view, column, arm.Input.Update.ArrayTargets[j], action)
					if err != nil {
						return "", err
					}
				}
				text, err := render(value)
				if err != nil {
					return "", err
				}
				sql.WriteString(column + " = " + text)
			}
		case parser.MergeInsertAction:
			syntax := action.syntax.(*parser.Merge).Arms[i]
			sql.WriteString("INSERT")
			if syntax.DefaultValues {
				sql.WriteString(" DEFAULT VALUES")
				continue
			}
			columns := make([]string, len(arm.Input.Targets))
			for j, slot := range arm.Input.Targets {
				columns[j], err = DeparseAttribute(view.MustRelation(b.Ref.Address), arm.Input.Relation.Attributes[slot])
				if err != nil {
					return "", err
				}
			}
			sql.WriteString(" (" + strings.Join(columns, ", ") + ")")
			if syntax.Override == parser.OverrideSystem {
				sql.WriteString(" OVERRIDING SYSTEM VALUE")
			}
			if syntax.Override == parser.OverrideUser {
				sql.WriteString(" OVERRIDING USER VALUE")
			}
			if len(arm.Input.Expressions) != 1 {
				return "", fmt.Errorf("stuntgres: missing MERGE INSERT values")
			}
			values := make([]string, len(arm.Input.Expressions[0]))
			for j, value := range arm.Input.Expressions[0] {
				values[j], err = render(value)
				if err != nil {
					return "", err
				}
			}
			sql.WriteString(" VALUES (" + strings.Join(values, ", ") + ")")
		default:
			return "", fmt.Errorf("stuntgres: invalid MERGE action recipe")
		}
	}
	return sql.String(), nil
}
