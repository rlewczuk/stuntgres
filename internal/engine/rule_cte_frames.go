package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func isolateRuleCTEFrames(view catalog.Read, original, action binding, syntax parser.Statement) (binding, error) {
	if len(original.CTEs) == 0 {
		return action, nil
	}
	local := len(action.CTEs) != 0
	visit := func(root plan.Node) {
		plan.VisitCTEQuery(root, func(node plan.Node) {
			if project, ok := node.(*plan.ProjectNode); ok && project.CTEOwner {
				local = true
			}
		}, func(expr.Expr) {}, true)
	}
	if action.Query != nil {
		visit(action.Query.Root)
	}
	if action.Update != nil {
		visit(action.Update.From)
		visit(&plan.ProjectNode{Expressions: action.Update.Assignments})
		if action.Update.Predicate != nil {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{action.Update.Predicate}})
		}
	}
	visit(action.Delete.Using)
	for _, row := range action.Expressions {
		for _, bound := range row {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	for _, bound := range action.Insert.Defaults {
		visit(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
	}
	if action.Returning != nil {
		visit(&plan.ProjectNode{Expressions: action.Returning.Expressions})
	}
	if action.Predicate != nil {
		visit(&plan.ProjectNode{Expressions: []expr.Expr{action.Predicate}})
	}
	if action.Insert.Conflict != nil {
		if action.Insert.Conflict.Predicate != nil {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{action.Insert.Conflict.Predicate}})
		}
		if action.Insert.Conflict.Update != nil {
			visit(&plan.ProjectNode{Expressions: action.Insert.Conflict.Update.Assignments})
		}
	}
	if !local {
		return action, nil
	}
	reserved := ruleBindingMaxFrame(original)
	ownerFrame := func(old uint32) uint32 {
		if old == 0 {
			return 0
		}
		return old + reserved + 1
	}
	frame := func(old uint32) uint32 {
		if old == 0 {
			return 0
		}
		if _, selection := syntax.(*parser.Select); !selection && old == 1 {
			return 1
		}
		return ownerFrame(old)
	}
	return mapRuleBindingRecipes(action, newRuleTreeMapper(view, frame, 0, original.CTEs, ruleTreeMapping{ownerFrame: ownerFrame}))
}
