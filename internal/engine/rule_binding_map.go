package engine

import (
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func mapRuleBindingRecipes(b binding, mapper ruleTreeMapper) (binding, error) {
	originalReturning := b.returning
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
	b.arrayTargets, err = targets(b.arrayTargets)
	if err != nil {
		return b, err
	}
	if b.query != nil {
		root, err := mapper.query(b.query.Root)
		if err != nil {
			return b, err
		}
		if root != b.query.Root {
			query := *b.query
			query.Root = root
			b.query = &query
		}
	}
	var rows [][]expr.Expr
	for i, row := range b.expressions {
		next, changed, err := list(row)
		if err != nil {
			return b, err
		}
		if changed {
			if rows == nil {
				rows = slices.Clone(b.expressions)
			}
			rows[i] = next
		}
	}
	if rows != nil {
		b.expressions = rows
	}
	b.insert.defaults, _, err = list(b.insert.defaults)
	if err != nil {
		return b, err
	}
	b.predicate, err = expression(b.predicate)
	if err != nil {
		return b, err
	}
	b.delete.using, err = mapper.query(b.delete.using)
	if err != nil {
		return b, err
	}
	if b.update != nil {
		update := *b.update
		update.arrayTargets, err = targets(update.arrayTargets)
		if err != nil {
			return b, err
		}
		var assignmentsChanged, predicateChanged bool
		update.assignments, assignmentsChanged, err = list(update.assignments)
		if err != nil {
			return b, err
		}
		update.predicate, predicateChanged, err = mapper.expression(update.predicate)
		if err != nil {
			return b, err
		}
		update.from, err = mapper.query(update.from)
		if err != nil {
			return b, err
		}
		if assignmentsChanged || predicateChanged || update.from != b.update.from || !slices.Equal(update.arrayTargets, b.update.arrayTargets) {
			b.update = &update
		}
	}
	if b.returning != nil {
		values, changed, err := list(b.returning.expressions)
		if err != nil {
			return b, err
		}
		if changed {
			returning := *b.returning
			returning.expressions = values
			b.returning = &returning
		}
	}
	if b.insert.conflict != nil {
		conflict := *insertConflictOf(b.insert.conflict)
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
			update.assignments, assignmentsChanged, err = list(update.assignments)
			if err != nil {
				return b, err
			}
			update.predicate, predicateChanged, err = mapper.expression(update.predicate)
			if err != nil {
				return b, err
			}
			update.arrayTargets, err = targets(update.arrayTargets)
			if err != nil {
				return b, err
			}
			if assignmentsChanged || predicateChanged || !slices.Equal(update.arrayTargets, conflict.update.arrayTargets) {
				conflict.update = &update
				conflictChanged = true
			}
		}
		if conflict.returning == originalReturning {
			if conflict.returning != b.returning {
				conflict.returning = b.returning
				conflictChanged = true
			}
		} else if conflict.returning != nil {
			values, changed, err := list(conflict.returning.expressions)
			if err != nil {
				return b, err
			}
			if changed {
				returning := *conflict.returning
				returning.expressions = values
				conflict.returning = &returning
				conflictChanged = true
			}
		}
		if conflictChanged {
			b.insert.conflict = insertConflictSlotOf(&conflict)
		}
	}
	var definitions []*plan.CTEDefinition
	for i, original := range b.ctes {
		next, err := mapper.definition(original)
		if err != nil {
			return b, err
		}
		if next != original {
			if definitions == nil {
				definitions = slices.Clone(b.ctes)
			}
			definitions[i] = next
		}
	}
	if definitions != nil {
		b.ctes = definitions
	}
	return b, nil
}
