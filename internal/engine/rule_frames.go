package engine

// Transplanted queries retain lexical identity, including expression-owned
// subqueries and CTE producers. This shares the existing copy-on-change walkers.
func rewriteRuleFrames(view CatalogRead, root LogicalNode, expr BoundExpr, frame func(uint32) uint32, retainedCTEs ...[]*cteDefinition) (LogicalNode, BoundExpr, error) {
	var retained []*cteDefinition
	if len(retainedCTEs) != 0 {
		retained = retainedCTEs[0]
	}
	mapper := newRuleTreeMapper(view, frame, 0, retained)
	mapped, err := mapper.query(root)
	if err != nil {
		return nil, nil, err
	}
	mappedExpr, _, err := mapper.expression(expr)
	return mapped, mappedExpr, err
}

func ruleBindingMaxFrame(b binding) uint32 {
	max := uint32(1)
	visit := func(root LogicalNode) {
		frames, _ := collectQueryFrames(root)
		for frame := range frames {
			if frame > max {
				max = frame
			}
		}
	}
	visitExpression := func(bound BoundExpr) {
		if bound != nil {
			visit(&projectNode{expressions: []BoundExpr{bound}})
		}
	}
	for _, definition := range b.ctes {
		if definition.owner > max {
			max = definition.owner
		}
		if definition.query != nil {
			visit(definition.query.Root)
		}
	}
	for _, expr := range b.defaults {
		visitExpression(expr)
	}
	visitExpression(b.predicate)
	if b.returning != nil {
		for _, expr := range b.returning.expressions {
			visitExpression(expr)
		}
	}
	if b.conflict != nil {
		for _, key := range b.conflict.keys {
			visitExpression(key.expression)
		}
		visitExpression(b.conflict.predicate)
		if b.conflict.update != nil {
			visitExpression(b.conflict.update.predicate)
			for _, expr := range b.conflict.update.assignments {
				visitExpression(expr)
			}
		}
	}
	if b.query != nil {
		visit(b.query.Root)
	}
	for _, row := range b.expressions {
		for _, bound := range row {
			visit(&projectNode{expressions: []BoundExpr{bound}})
		}
	}
	if b.update != nil {
		visit(b.update.from)
		visitExpression(b.update.predicate)
		for _, bound := range b.update.assignments {
			visit(&projectNode{expressions: []BoundExpr{bound}})
		}
	}
	visit(b.deleteUsing)
	return max
}

func rewriteRuleOccurrences(view CatalogRead, root LogicalNode, expr BoundExpr, offset int) (LogicalNode, BoundExpr, error) {
	if offset == 0 {
		return root, expr, nil
	}
	mapper := newRuleTreeMapper(view, nil, offset, nil)
	mapped, err := mapper.query(root)
	if err != nil {
		return nil, nil, err
	}
	mappedExpr, _, err := mapper.expression(expr)
	return mapped, mappedExpr, err
}

func offsetRuleSources(view CatalogRead, b binding, offset int, retainedCTEs ...[]*cteDefinition) (binding, error) {
	if offset == 0 {
		return b, nil
	}
	var retained []*cteDefinition
	if len(retainedCTEs) != 0 {
		retained = retainedCTEs[0]
	}
	return mapRuleBindingRecipes(b, newRuleTreeMapper(view, nil, offset, retained))
}
