package stored

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func MapRuleBindingRecipes(b analyze.Binding, mapper ruleTreeMapper) (analyze.Binding, error) {
	originalReturning := b.Returning
	expression := func(value expr.Expr) (expr.Expr, error) { next, _, err := mapper.Expression(value); return next, err }
	list := func(values []expr.Expr) ([]expr.Expr, bool, error) {
		var copied []expr.Expr
		for i, value := range values {
			next, changed, err := mapper.Expression(value)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if copied == nil {
					copied = slices.Clone(values)
				}
				copied[i] = next
			}
		}
		if copied == nil {
			return values, false, nil
		}
		return copied, true, nil
	}
	targets := func(values []*plan.ArrayAssignmentTarget) ([]*plan.ArrayAssignmentTarget, error) {
		return MapRuleAssignmentTargets(values, mapper.Expression, func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil })
	}
	var err error
	b.ArrayTargets, err = targets(b.ArrayTargets)
	if err != nil {
		return b, err
	}
	if b.Query != nil {
		root, err := mapper.Query(b.Query.Root)
		if err != nil {
			return b, err
		}
		if root != b.Query.Root {
			query := *b.Query
			query.Root = root
			b.Query = &query
		}
	}
	var rows [][]expr.Expr
	for i, row := range b.Expressions {
		next, changed, err := list(row)
		if err != nil {
			return b, err
		}
		if changed {
			if rows == nil {
				rows = slices.Clone(b.Expressions)
			}
			rows[i] = next
		}
	}
	if rows != nil {
		b.Expressions = rows
	}
	b.Insert.Defaults, _, err = list(b.Insert.Defaults)
	if err != nil {
		return b, err
	}
	b.Predicate, err = expression(b.Predicate)
	if err != nil {
		return b, err
	}
	b.Delete.Using, err = mapper.Query(b.Delete.Using)
	if err != nil {
		return b, err
	}
	if b.Update != nil {
		update := *b.Update
		update.ArrayTargets, err = targets(update.ArrayTargets)
		if err != nil {
			return b, err
		}
		var assignmentsChanged, predicateChanged bool
		update.Assignments, assignmentsChanged, err = list(update.Assignments)
		if err != nil {
			return b, err
		}
		update.Predicate, predicateChanged, err = mapper.Expression(update.Predicate)
		if err != nil {
			return b, err
		}
		update.From, err = mapper.Query(update.From)
		if err != nil {
			return b, err
		}
		if assignmentsChanged || predicateChanged || update.From != b.Update.From || !slices.Equal(update.ArrayTargets, b.Update.ArrayTargets) {
			b.Update = &update
		}
	}
	if b.Returning != nil {
		values, changed, err := list(b.Returning.Expressions)
		if err != nil {
			return b, err
		}
		if changed {
			returning := *b.Returning
			returning.Expressions = values
			b.Returning = &returning
		}
	}
	if b.Insert.Conflict != nil {
		conflict := *b.Insert.Conflict
		var keys []analyze.ConflictKey
		for i, key := range conflict.Keys {
			next, changed, err := mapper.Expression(key.Expression)
			if err != nil {
				return b, err
			}
			if changed {
				if keys == nil {
					keys = slices.Clone(conflict.Keys)
				}
				keys[i].Expression = next
			}
		}
		if keys != nil {
			conflict.Keys = keys
		}
		conflictChanged := keys != nil
		var predicateChanged bool
		conflict.Predicate, predicateChanged, err = mapper.Expression(conflict.Predicate)
		if err != nil {
			return b, err
		}
		conflictChanged = conflictChanged || predicateChanged
		if conflict.Update != nil {
			update := *conflict.Update
			var assignmentsChanged, predicateChanged bool
			update.Assignments, assignmentsChanged, err = list(update.Assignments)
			if err != nil {
				return b, err
			}
			update.Predicate, predicateChanged, err = mapper.Expression(update.Predicate)
			if err != nil {
				return b, err
			}
			update.ArrayTargets, err = targets(update.ArrayTargets)
			if err != nil {
				return b, err
			}
			if assignmentsChanged || predicateChanged || !slices.Equal(update.ArrayTargets, conflict.Update.ArrayTargets) {
				conflict.Update = &update
				conflictChanged = true
			}
		}
		if conflict.Returning == originalReturning {
			if conflict.Returning != b.Returning {
				conflict.Returning = b.Returning
				conflictChanged = true
			}
		} else if conflict.Returning != nil {
			values, changed, err := list(conflict.Returning.Expressions)
			if err != nil {
				return b, err
			}
			if changed {
				returning := *conflict.Returning
				returning.Expressions = values
				conflict.Returning = &returning
				conflictChanged = true
			}
		}
		if conflictChanged {
			b.Insert.Conflict = &conflict
		}
	}
	var definitions []*plan.CTEDefinition
	for i, original := range b.CTEs {
		next, err := mapper.Definition(original)
		if err != nil {
			return b, err
		}
		if next != original {
			if definitions == nil {
				definitions = slices.Clone(b.CTEs)
			}
			definitions[i] = next
		}
	}
	if definitions != nil {
		b.CTEs = definitions
	}
	return b, nil
}
