package stored

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func RefreshRuleBinding(catalog catalog.Read, b analyze.Binding, current *catalog.Relation) (analyze.Binding, error) {
	old := b.Relation
	if slices.Equal(old.Attributes, current.Attributes) {
		b.Relation = current
		return b, nil
	}
	slots := func(original []int) ([]int, error) {
		next := slices.Clone(original)
		for i, slot := range original {
			if slot < 0 || slot >= len(old.Attributes) {
				return nil, ids.ErrCatalogReplaced
			}
			next[i] = slices.Index(current.Attributes, old.Attributes[slot])
			if next[i] < 0 {
				return nil, ids.ErrCatalogReplaced
			}
		}
		return next, nil
	}
	var err error
	b.Targets, err = slots(b.Targets)
	if err != nil {
		return b, err
	}
	b.UsedColumns, err = slots(b.UsedColumns)
	if err != nil {
		return b, err
	}
	if b.Insert.Conflict != nil {
		conflict, err := refreshRuleConflict(catalog, b, current, slots)
		if err != nil {
			return b, err
		}
		b.Insert.Conflict = conflict
	}
	leaf := func(expr expr.Expr) (expr.Expr, bool, error) {
		if column, ok := expr.(plan.ColumnExpr); ok && column.Scope == 1 && column.Frame == 1 && column.Slot >= len(old.Columns) {
			column.Slot += len(current.Columns) - len(old.Columns)
			if column.Virtual != nil {
				column.RowStart += len(current.Columns) - len(old.Columns)
			}
			return column, true, nil
		}
		column, ok := expr.(plan.ColumnExpr)
		if !ok || column.Origin.Address.ObjectID != b.Ref.Address.ObjectID {
			return expr, false, nil
		}
		if !(column.Scope == 0 && column.Frame == 0) && !(column.Scope == 1 && column.Frame == 1 && column.Slot < len(old.Columns)) {
			return expr, false, nil
		}
		column.Slot = slices.Index(current.Attributes, column.Origin.Address.SubID)
		if column.Slot < 0 {
			return nil, false, ids.ErrCatalogReplaced
		}
		return column, true, nil
	}
	mapExpr := func(expr expr.Expr) (expr.Expr, error) {
		next, _, err := RewriteViewExpression(catalog, expr, nil, leaf)
		return next, err
	}
	mapTargets := func(targets []*plan.ArrayAssignmentTarget) ([]*plan.ArrayAssignmentTarget, error) {
		return MapRuleAssignmentTargets(targets, func(expr expr.Expr) (expr.Expr, bool, error) { return RewriteViewExpression(catalog, expr, nil, leaf) }, func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil })
	}
	b.ArrayTargets, err = mapTargets(b.ArrayTargets)
	if err != nil {
		return b, err
	}
	if b.Query != nil {
		next := *b.Query
		next.Root, err = plan.RewriteQueryExpressions(next.Root, func(expr expr.Expr) (expr.Expr, bool, error) { return RewriteViewExpression(catalog, expr, nil, leaf) })
		if err != nil {
			return b, err
		}
		b.Query = &next
	}
	if b.Returning != nil {
		next := *b.Returning
		next.Expressions = slices.Clone(next.Expressions)
		for i, expr := range next.Expressions {
			next.Expressions[i], err = mapExpr(expr)
			if err != nil {
				return b, err
			}
		}
		b.Returning = &next
	}
	if b.Update != nil {
		u := *b.Update
		u.Assignments = slices.Clone(u.Assignments)
		u.ArrayTargets, err = mapTargets(u.ArrayTargets)
		if err != nil {
			return b, err
		}
		for i, expr := range u.Assignments {
			u.Assignments[i], _, err = RewriteViewExpression(catalog, expr, nil, leaf)
			if err != nil {
				return b, err
			}
		}
		u.Predicate, _, err = RewriteViewExpression(catalog, u.Predicate, nil, leaf)
		if err != nil {
			return b, err
		}
		b.Update = &u
	}
	b.Predicate, _, err = RewriteViewExpression(catalog, b.Predicate, nil, leaf)
	if err != nil {
		return b, err
	}
	b.Relation = current
	return b, nil
}

func refreshRuleConflict(catalog catalog.Read, b analyze.Binding, current *catalog.Relation, slots func([]int) ([]int, error)) (*analyze.InsertConflict, error) {
	c := *b.Insert.Conflict
	var err error
	c.Targets, err = slots(c.Targets)
	if err != nil {
		return nil, err
	}
	c.UpdateTargets, err = slots(c.UpdateTargets)
	if err != nil {
		return nil, err
	}
	c.Reads, err = slots(c.Reads)
	if err != nil {
		return nil, err
	}
	c.ActionReads, err = slots(c.ActionReads)
	if err != nil {
		return nil, err
	}
	oldWidth, width := len(b.Relation.Attributes), len(current.Attributes)
	leaf := func(expr expr.Expr) (expr.Expr, bool, error) {
		column, ok := expr.(plan.ColumnExpr)
		if !ok || column.Origin.Address.ClassID != b.Ref.Address.ClassID || column.Origin.Address.ObjectID != b.Ref.Address.ObjectID {
			return expr, false, nil
		}
		if !(column.Scope == 0 && column.Frame == 0) && !(column.Scope == 1 && column.Frame == 1) {
			return expr, false, nil
		}
		slot := current.IndexForAttribute(column.Origin.Address.SubID)
		if slot < 0 {
			return nil, false, ids.ErrCatalogReplaced
		}
		if column.Scope == 1 && column.Frame == 1 && column.Slot >= oldWidth {
			slot += width
		}
		column.Slot = slot
		if column.Virtual != nil {
			if column.RowStart >= oldWidth {
				column.RowStart = width
			}
			column.RowWidth = width
		}
		return column, true, nil
	}
	mapper := NewRuleTreeMapper(catalog, nil, 0, nil, RuleTreeMapping{Leaf: leaf})
	mapped, err := MapRuleBindingRecipes(analyze.Binding{Insert: analyze.InsertBinding{Conflict: &c}}, mapper)
	if err != nil {
		return nil, err
	}
	c = *mapped.Insert.Conflict
	c.Width = width
	return &c, nil
}
