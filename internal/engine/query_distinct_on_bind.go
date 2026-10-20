package engine

import (
	"strconv"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Identities are analysis targets, not structural expression cache identities.
func (b *expressionBinder) resolveDistinctOn(s *parser.Select, order []plan.SortKey, projection []expr.Expr, names []string) ([]plan.SortKey, []plan.Grouping, error) {
	for i := range order {
		if err := bindOnOrdering(&order[i]); err != nil {
			return nil, nil, err
		}
	}
	raw := make([]parser.OrderKey, len(s.Distinct.On))
	for i, e := range s.Distinct.On {
		raw[i] = parser.OrderKey{Expr: e, Pos: parser.ExpressionStart(e)}
	}
	b.Clause.AggregateClause = "DISTINCT ON"
	on, err := b.BindOrder(&parser.Order{Keys: raw}, projection, names, "DISTINCT ON")
	if err != nil {
		return nil, nil, err
	}
	targets := append([]expr.Expr(nil), projection...)
	identify := func(key *plan.SortKey) {
		if key.OutputOrdinal != 0 {
			key.TargetIdentity = key.OutputOrdinal
			return
		}
		for i, e := range targets {
			if b.SameQueryTarget(e, key.Expression) {
				key.TargetIdentity = i + 1
				if i < len(projection) {
					key.OutputOrdinal = i + 1
				}
				return
			}
		}
		targets = append(targets, key.Expression)
		key.TargetIdentity = len(targets)
	}
	for i := range order {
		identify(&order[i])
	}
	for i := range on {
		identify(&on[i])
	}
	matched := make([]bool, len(on))
	gap := false
	used := make(map[int]bool)
	for _, key := range order {
		if used[key.TargetIdentity] {
			continue
		}
		used[key.TargetIdentity] = true
		found := false
		for i, k := range on {
			if key.TargetIdentity == k.TargetIdentity {
				if gap {
					return nil, nil, expr.ErrorAt("42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions", raw[i].Pos)
				}
				matched[i] = true
				found = true
			}
		}
		if !found {
			gap = true
		}
	}
	if gap {
		for i, found := range matched {
			if !found {
				return nil, nil, expr.ErrorAt("42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions", raw[i].Pos)
			}
		}
	}
	families := make([]plan.Grouping, len(on))
	for i, k := range on {
		families[i], err = b.bindDistinctGrouping(k.Expression)
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok {
				copy := *d
				copy.Position = strconv.Itoa(raw[i].Pos.Character)
				err = &copy
			}
			return nil, nil, err
		}
	}
	return on, families, nil
}

func bindOnOrdering(key *plan.SortKey) error {
	if key.Expression.CollationIndeterminate() {
		return nil
	}
	provider, err := sqltype.BindOrdering(key.Expression.ResultUsage())
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		if key.Position > 0 {
			copy.Position = strconv.Itoa(key.Position)
		}
		return &copy
	}
	key.Comparison = provider
	return err
}

func (b *expressionBinder) FinishDistinctOn(root plan.Node, order, on []plan.SortKey, families []plan.Grouping, projection []expr.Expr) (plan.Node, error) {
	// Deduplicate only resolved ORDER targets. Keep raw entries for capture.
	effective := make([]plan.SortKey, 0, len(order))
	seen := make(map[int]bool)
	for _, k := range order {
		if !seen[k.TargetIdentity] {
			effective = append(effective, k)
			seen[k.TargetIdentity] = true
		}
	}
	var err error
	root, err = b.FinishOrder(root, effective, nil)
	if err != nil {
		return nil, err
	}
	sorted, _ := root.(*plan.SortNode)
	cacheWidth := 0
	if sorted != nil {
		cacheWidth = sorted.CacheWidth
	}
	var extra []expr.Expr
	required := make([]plan.SortKey, len(effective)+len(on))
	copy(required, effective)
	copy(required[len(effective):], on)
	for i := range required {
		k := &required[i]
		if i < len(effective) {
			continue
		}
		k.Cache = -1
		for j := range i {
			if required[j].TargetIdentity == k.TargetIdentity {
				k.Expression = required[j].Expression
				k.Cache = required[j].Cache
				break
			}
		}
		if k.Cache >= 0 {
			continue
		}
		switch k.Expression.(type) {
		case plan.ColumnExpr, plan.GroupExpr:
			continue
		}
		k.Cache = cacheWidth
		cacheWidth++
		extra = append(extra, k.Expression)
	}
	// Exact demanded output slots win; only undemanded payload may share a
	// structurally equivalent required datum (the observed random x/y case).
	for j, expr := range projection {
		chosen := -1
		demanded := false
		for i, k := range required {
			if k.TargetIdentity == j+1 {
				chosen = i
				demanded = true
				break
			}
		}
		if !demanded {
			for i, k := range required {
				if b.SameGroupExpression(expr, k.Expression) {
					chosen = i
					break
				}
			}
		}
		if chosen >= 0 && required[chosen].Cache >= 0 {
			projection[j] = distinctOnCached(expr, required[chosen].Cache)
			if sorted != nil && chosen < len(effective) {
				sorted.Keys[chosen].Output = true
			}
		}
	}
	if sorted != nil {
		sorted.Outputs = append(sorted.Outputs, extra...)
		sorted.CacheWidth = cacheWidth
	} else if len(extra) != 0 {
		root = &plan.EvaluationNode{QuerySchema: root.Schema(), Child: root, Expressions: extra, Base: 0}
	}
	// Cheap nonvolatile outputs are required below ON, not complete-row keys.
	if sorted != nil {
		root = b.ScheduleQueryProjection(root, projection, false)
		cacheWidth = sorted.CacheWidth
	} else {
		early := make([]expr.Expr, 0)
		for i, e := range projection {
			if exec.QuerySetDepth(e) != 0 {
				continue
			}
			if e.Properties().Volatility == expr.Volatile || e.ResultUsage().Type == nil {
				continue
			}
			switch e.(type) {
			case plan.ColumnExpr, plan.GroupExpr, plan.CachedOrderExpr, plan.LiteralExpr, plan.ParameterExpr, plan.UnknownOutput:
				continue
			}
			projection[i] = distinctOnCached(e, cacheWidth+len(early))
			early = append(early, e)
		}
		if len(early) != 0 {
			root = &plan.EvaluationNode{QuerySchema: root.Schema(), Child: root, Expressions: early, Base: cacheWidth}
			cacheWidth += len(early)
		}
	}
	expressions := make([]expr.Expr, 0, len(on))
	grouping := make([]plan.Grouping, 0, len(on))
	clear(seen)
	for i, k := range required[len(effective):] {
		if seen[k.TargetIdentity] {
			continue
		}
		seen[k.TargetIdentity] = true
		e := k.Expression
		if k.Cache >= 0 {
			e = distinctOnCached(e, k.Cache)
		}
		expressions = append(expressions, e)
		grouping = append(grouping, families[i])
	}
	for i, e := range projection {
		if cached, ok := e.(plan.CachedOrderExpr); ok {
			cached.Property.StageDependent = true
			projection[i] = cached
		}
	}
	return &plan.DistinctOnNode{QuerySchema: root.Schema(), Child: root, Expressions: expressions, Grouping: grouping}, nil
}

func distinctOnCached(expression expr.Expr, slot int) plan.CachedOrderExpr {
	info := expr.InfoOf(expression)
	info.Property.StageDependent = true
	return plan.CachedOrderExpr{Info: info, Slot: slot}
}
