package engine

import (
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func mapRuleBindingRecipes(b binding, mapper ruleTreeMapper) (binding, error) {
	originalReturning := b.Returning
	expression := func(value expr.Expr) (expr.Expr, error) { next, _, err := mapper.expression(value); return next, err }
	list := func(values []expr.Expr) ([]expr.Expr, bool, error) {
		var copied []expr.Expr
		for i, value := range values {
			next, changed, err := mapper.expression(value)
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
		return mapRuleAssignmentTargets(values, mapper.expression, func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil })
	}
	var err error
	b.ArrayTargets, err = targets(b.ArrayTargets)
	if err != nil {
		return b, err
	}
	if b.Query != nil {
		root, err := mapper.query(b.Query.Root)
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
	b.Delete.Using, err = mapper.query(b.Delete.Using)
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
		update.Predicate, predicateChanged, err = mapper.expression(update.Predicate)
		if err != nil {
			return b, err
		}
		update.From, err = mapper.query(update.From)
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
		conflict := *insertConflictOf(b.Insert.Conflict)
		var keys []conflictKey
		for i, key := range conflict.keys {
			next, changed, err := mapper.expression(key.expression)
			if err != nil {
				return b, err
			}
			if changed {
				if keys == nil {
					keys = slices.Clone(conflict.keys)
				}
				keys[i].expression = next
			}
		}
		if keys != nil {
			conflict.keys = keys
		}
		conflictChanged := keys != nil
		var predicateChanged bool
		conflict.predicate, predicateChanged, err = mapper.expression(conflict.predicate)
		if err != nil {
			return b, err
		}
		conflictChanged = conflictChanged || predicateChanged
		if conflict.update != nil {
			update := *conflict.update
			var assignmentsChanged, predicateChanged bool
			update.Assignments, assignmentsChanged, err = list(update.Assignments)
			if err != nil {
				return b, err
			}
			update.Predicate, predicateChanged, err = mapper.expression(update.Predicate)
			if err != nil {
				return b, err
			}
			update.ArrayTargets, err = targets(update.ArrayTargets)
			if err != nil {
				return b, err
			}
			if assignmentsChanged || predicateChanged || !slices.Equal(update.ArrayTargets, conflict.update.ArrayTargets) {
				conflict.update = &update
				conflictChanged = true
			}
		}
		if conflict.returning == originalReturning {
			if conflict.returning != b.Returning {
				conflict.returning = b.Returning
				conflictChanged = true
			}
		} else if conflict.returning != nil {
			values, changed, err := list(conflict.returning.Expressions)
			if err != nil {
				return b, err
			}
			if changed {
				returning := *conflict.returning
				returning.Expressions = values
				conflict.returning = &returning
				conflictChanged = true
			}
		}
		if conflictChanged {
			b.Insert.Conflict = insertConflictSlotOf(&conflict)
		}
	}
	var definitions []*plan.CTEDefinition
	for i, original := range b.CTEs {
		next, err := mapper.definition(original)
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
