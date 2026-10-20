package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func isolateRuleCTEFrames(view catalog.Read, original, action binding, syntax parser.Statement) (binding, error) {
	if len(original.ctes) == 0 {
		return action, nil
	}
	local := len(action.ctes) != 0
	visit := func(root plan.Node) {
		plan.VisitCTEQuery(root, func(node plan.Node) {
			if project, ok := node.(*plan.ProjectNode); ok && project.CTEOwner {
				local = true
			}
		}, func(expr.Expr) {}, true)
	}
	if action.query != nil {
		visit(action.query.Root)
	}
	if action.update != nil {
		visit(action.update.from)
		visit(&plan.ProjectNode{Expressions: action.update.assignments})
		if action.update.predicate != nil {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{action.update.predicate}})
		}
	}
	visit(action.delete.using)
	for _, row := range action.expressions {
		for _, bound := range row {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	for _, bound := range action.insert.defaults {
		visit(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
	}
	if action.returning != nil {
		visit(&plan.ProjectNode{Expressions: action.returning.expressions})
	}
	if action.predicate != nil {
		visit(&plan.ProjectNode{Expressions: []expr.Expr{action.predicate}})
	}
	if action.insert.conflict != nil {
		if insertConflictOf(action.insert.conflict).predicate != nil {
			visit(&plan.ProjectNode{Expressions: []expr.Expr{insertConflictOf(action.insert.conflict).predicate}})
		}
		if insertConflictOf(action.insert.conflict).update != nil {
			visit(&plan.ProjectNode{Expressions: insertConflictOf(action.insert.conflict).update.assignments})
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
	return mapRuleBindingRecipes(action, newRuleTreeMapper(view, frame, 0, original.ctes, ruleTreeMapping{ownerFrame: ownerFrame}))
}
