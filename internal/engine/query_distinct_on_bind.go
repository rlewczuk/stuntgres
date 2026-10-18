package engine

import (
	"strconv"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Identities are analysis targets, not structural expression cache identities.
func (b *expressionBinder) resolveDistinctOn(s *parser.Select, order []sortKey, projection []expr.Expr, names []string) ([]sortKey, []queryGrouping, error) {
	for i := range order {
		if err := bindOnOrdering(&order[i]); err != nil {
			return nil, nil, err
		}
	}
	raw := make([]parser.OrderKey, len(s.Distinct.On))
	for i, e := range s.Distinct.On {
		raw[i] = parser.OrderKey{Expr: e, Pos: parser.ExpressionStart(e)}
	}
	b.aggregateClause = "DISTINCT ON"
	on, err := b.bindOrder(&parser.Order{Keys: raw}, projection, names, "DISTINCT ON")
	if err != nil {
		return nil, nil, err
	}
	targets := append([]expr.Expr(nil), projection...)
	identify := func(key *sortKey) {
		if key.outputOrdinal != 0 {
			key.targetIdentity = key.outputOrdinal
			return
		}
		for i, e := range targets {
			if b.sameQueryTarget(e, key.expression) {
				key.targetIdentity = i + 1
				if i < len(projection) {
					key.outputOrdinal = i + 1
				}
				return
			}
		}
		targets = append(targets, key.expression)
		key.targetIdentity = len(targets)
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
		if used[key.targetIdentity] {
			continue
		}
		used[key.targetIdentity] = true
		found := false
		for i, k := range on {
			if key.targetIdentity == k.targetIdentity {
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
	families := make([]queryGrouping, len(on))
	for i, k := range on {
		families[i], err = b.bindDistinctGrouping(k.expression)
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

func bindOnOrdering(key *sortKey) error {
	if key.expression.CollationIndeterminate() {
		return nil
	}
	provider, err := sqltype.BindOrdering(key.expression.ResultUsage())
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		if key.position > 0 {
			copy.Position = strconv.Itoa(key.position)
		}
		return &copy
	}
	key.comparison = provider
	return err
}

func (b *expressionBinder) finishDistinctOn(root LogicalNode, order, on []sortKey, families []queryGrouping, projection []expr.Expr) (LogicalNode, error) {
	// Deduplicate only resolved ORDER targets. Keep raw entries for capture.
	effective := make([]sortKey, 0, len(order))
	seen := make(map[int]bool)
	for _, k := range order {
		if !seen[k.targetIdentity] {
			effective = append(effective, k)
			seen[k.targetIdentity] = true
		}
	}
	var err error
	root, err = b.finishOrder(root, effective, nil)
	if err != nil {
		return nil, err
	}
	sorted, _ := root.(*sortNode)
	cacheWidth := 0
	if sorted != nil {
		cacheWidth = sorted.cacheWidth
	}
	var extra []expr.Expr
	required := make([]sortKey, len(effective)+len(on))
	copy(required, effective)
	copy(required[len(effective):], on)
	for i := range required {
		k := &required[i]
		if i < len(effective) {
			continue
		}
		k.cache = -1
		for j := range i {
			if required[j].targetIdentity == k.targetIdentity {
				k.expression = required[j].expression
				k.cache = required[j].cache
				break
			}
		}
		if k.cache >= 0 {
			continue
		}
		switch k.expression.(type) {
		case columnExpression, groupExpression:
			continue
		}
		k.cache = cacheWidth
		cacheWidth++
		extra = append(extra, k.expression)
	}
	// Exact demanded output slots win; only undemanded payload may share a
	// structurally equivalent required datum (the observed random x/y case).
	for j, expr := range projection {
		chosen := -1
		demanded := false
		for i, k := range required {
			if k.targetIdentity == j+1 {
				chosen = i
				demanded = true
				break
			}
		}
		if !demanded {
			for i, k := range required {
				if b.sameGroupExpression(expr, k.expression) {
					chosen = i
					break
				}
			}
		}
		if chosen >= 0 && required[chosen].cache >= 0 {
			projection[j] = distinctOnCached(expr, required[chosen].cache)
			if sorted != nil && chosen < len(effective) {
				sorted.keys[chosen].output = true
			}
		}
	}
	if sorted != nil {
		sorted.outputs = append(sorted.outputs, extra...)
		sorted.cacheWidth = cacheWidth
	} else if len(extra) != 0 {
		root = &evaluationNode{querySchema: root.Schema(), child: root, expressions: extra, base: 0}
	}
	// Cheap nonvolatile outputs are required below ON, not complete-row keys.
	if sorted != nil {
		root = b.scheduleQueryProjection(root, projection, false)
		cacheWidth = sorted.cacheWidth
	} else {
		early := make([]expr.Expr, 0)
		for i, e := range projection {
			if querySetDepth(e) != 0 {
				continue
			}
			if e.Properties().Volatility == expr.Volatile || e.ResultUsage().Type == nil {
				continue
			}
			switch e.(type) {
			case columnExpression, groupExpression, cachedOrderExpression, literalExpression, parameterExpression, unknownOutput:
				continue
			}
			projection[i] = distinctOnCached(e, cacheWidth+len(early))
			early = append(early, e)
		}
		if len(early) != 0 {
			root = &evaluationNode{querySchema: root.Schema(), child: root, expressions: early, base: cacheWidth}
			cacheWidth += len(early)
		}
	}
	expressions := make([]expr.Expr, 0, len(on))
	grouping := make([]queryGrouping, 0, len(on))
	clear(seen)
	for i, k := range required[len(effective):] {
		if seen[k.targetIdentity] {
			continue
		}
		seen[k.targetIdentity] = true
		e := k.expression
		if k.cache >= 0 {
			e = distinctOnCached(e, k.cache)
		}
		expressions = append(expressions, e)
		grouping = append(grouping, families[i])
	}
	for i, e := range projection {
		if cached, ok := e.(cachedOrderExpression); ok {
			cached.Property.StageDependent = true
			projection[i] = cached
		}
	}
	return &distinctOnNode{querySchema: root.Schema(), child: root, expressions: expressions, grouping: grouping}, nil
}

func distinctOnCached(expression expr.Expr, slot int) cachedOrderExpression {
	info := expr.InfoOf(expression)
	info.Property.StageDependent = true
	return cachedOrderExpression{Info: info, slot: slot}
}
