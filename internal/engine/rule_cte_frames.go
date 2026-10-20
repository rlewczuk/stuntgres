package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func isolateRuleCTEFrames(view catalog.Read, original, action binding, syntax parser.Statement) (binding, error) {
	if len(original.ctes) == 0 {
		return action, nil
	}
	local := len(action.ctes) != 0
	visit := func(root LogicalNode) {
		visitCTEQuery(root, func(node LogicalNode) {
			if project, ok := node.(*projectNode); ok && project.CTEOwner {
				local = true
			}
		}, func(expr.Expr) {}, true)
	}
	if action.query != nil {
		visit(action.query.Root)
	}
	if action.update != nil {
		visit(action.update.from)
		visit(&projectNode{Expressions: action.update.assignments})
		if action.update.predicate != nil {
			visit(&projectNode{Expressions: []expr.Expr{action.update.predicate}})
		}
	}
	visit(action.delete.using)
	for _, row := range action.expressions {
		for _, bound := range row {
			visit(&projectNode{Expressions: []expr.Expr{bound}})
		}
	}
	for _, bound := range action.insert.defaults {
		visit(&projectNode{Expressions: []expr.Expr{bound}})
	}
	if action.returning != nil {
		visit(&projectNode{Expressions: action.returning.expressions})
	}
	if action.predicate != nil {
		visit(&projectNode{Expressions: []expr.Expr{action.predicate}})
	}
	if action.insert.conflict != nil {
		if action.insert.conflict.predicate != nil {
			visit(&projectNode{Expressions: []expr.Expr{action.insert.conflict.predicate}})
		}
		if action.insert.conflict.update != nil {
			visit(&projectNode{Expressions: action.insert.conflict.update.assignments})
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
