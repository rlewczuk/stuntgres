package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Transplanted queries retain lexical identity, including expression-owned
// subqueries and CTE producers. This shares the existing copy-on-change walkers.
func rewriteRuleFrames(view catalog.Read, root plan.Node, expr expr.Expr, frame func(uint32) uint32, retainedCTEs ...[]*plan.CTEDefinition) (plan.Node, expr.Expr, error) {
	var retained []*plan.CTEDefinition
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
	visit := func(root plan.Node) {
		frames, _ := collectQueryFrames(root)
		for frame := range frames {
			if frame > max {
				max = frame
			}
		}
	}
	visitExpression := func(bound expr.Expr) {
		if bound != nil {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	for _, definition := range b.ctes {
		if definition.Owner > max {
			max = definition.Owner
		}
		if definition.Query != nil {
			visit(definition.Query.Root)
		}
	}
	for _, expr := range b.insert.defaults {
		visitExpression(expr)
	}
	visitExpression(b.predicate)
	if b.returning != nil {
		for _, expr := range b.returning.expressions {
			visitExpression(expr)
		}
	}
	if b.insert.conflict != nil {
		for _, key := range b.insert.conflict.keys {
			visitExpression(key.expression)
		}
		visitExpression(b.insert.conflict.predicate)
		if b.insert.conflict.update != nil {
			visitExpression(b.insert.conflict.update.predicate)
			for _, expr := range b.insert.conflict.update.assignments {
				visitExpression(expr)
			}
		}
	}
	if b.query != nil {
		visit(b.query.Root)
	}
	for _, row := range b.expressions {
		for _, bound := range row {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	if b.update != nil {
		visit(b.update.from)
		visitExpression(b.update.predicate)
		for _, bound := range b.update.assignments {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	visit(b.delete.using)
	return max
}

func rewriteRuleOccurrences(view catalog.Read, root plan.Node, expr expr.Expr, offset int) (plan.Node, expr.Expr, error) {
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

func offsetRuleSources(view catalog.Read, b binding, offset int, retainedCTEs ...[]*plan.CTEDefinition) (binding, error) {
	if offset == 0 {
		return b, nil
	}
	var retained []*plan.CTEDefinition
	if len(retainedCTEs) != 0 {
		retained = retainedCTEs[0]
	}
	return mapRuleBindingRecipes(b, newRuleTreeMapper(view, nil, offset, retained))
}
