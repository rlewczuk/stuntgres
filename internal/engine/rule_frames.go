package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// Transplanted queries retain lexical identity, including expression-owned
// subqueries and CTE producers. This shares the existing copy-on-change walkers.
func rewriteRuleFrames(view catalog.Read, root plan.Node, expr expr.Expr, frame func(uint32) uint32, retainedCTEs ...[]*plan.CTEDefinition) (plan.Node, expr.Expr, error) {
	var retained []*plan.CTEDefinition
	if len(retainedCTEs) != 0 {
		retained = retainedCTEs[0]
	}
	mapper := stored.NewRuleTreeMapper(view, frame, 0, retained)
	mapped, err := mapper.Query(root)
	if err != nil {
		return nil, nil, err
	}
	mappedExpr, _, err := mapper.Expression(expr)
	return mapped, mappedExpr, err
}

func ruleBindingMaxFrame(b analyze.Binding) uint32 {
	max := uint32(1)
	visit := func(root plan.Node) {
		frames, _ := analyze.CollectQueryFrames(root)
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
	for _, definition := range b.CTEs {
		if definition.Owner > max {
			max = definition.Owner
		}
		if definition.Query != nil {
			visit(definition.Query.Root)
		}
	}
	for _, expr := range b.Insert.Defaults {
		visitExpression(expr)
	}
	visitExpression(b.Predicate)
	if b.Returning != nil {
		for _, expr := range b.Returning.Expressions {
			visitExpression(expr)
		}
	}
	if b.Insert.Conflict != nil {
		for _, key := range b.Insert.Conflict.Keys {
			visitExpression(key.Expression)
		}
		visitExpression(b.Insert.Conflict.Predicate)
		if b.Insert.Conflict.Update != nil {
			visitExpression(b.Insert.Conflict.Update.Predicate)
			for _, expr := range b.Insert.Conflict.Update.Assignments {
				visitExpression(expr)
			}
		}
	}
	if b.Query != nil {
		visit(b.Query.Root)
	}
	for _, row := range b.Expressions {
		for _, bound := range row {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	if b.Update != nil {
		visit(b.Update.From)
		visitExpression(b.Update.Predicate)
		for _, bound := range b.Update.Assignments {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	visit(b.Delete.Using)
	return max
}

func offsetRuleSources(view catalog.Read, b analyze.Binding, offset int, retainedCTEs ...[]*plan.CTEDefinition) (analyze.Binding, error) {
	if offset == 0 {
		return b, nil
	}
	var retained []*plan.CTEDefinition
	if len(retainedCTEs) != 0 {
		retained = retainedCTEs[0]
	}
	return stored.MapRuleBindingRecipes(b, stored.NewRuleTreeMapper(view, nil, offset, retained))
}
