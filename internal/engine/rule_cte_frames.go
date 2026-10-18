package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func isolateRuleCTEFrames(view CatalogRead, original, action binding, syntax parser.Statement) (binding, error) {
	if len(original.ctes) == 0 {
		return action, nil
	}
	local := len(action.ctes) != 0
	visit := func(root LogicalNode) {
		visitCTEQuery(root, func(node LogicalNode) {
			if project, ok := node.(*projectNode); ok && project.cteOwner {
				local = true
			}
		}, func(expr.Expr) {}, true)
	}
	if action.query != nil {
		visit(action.query.Root)
	}
	if action.update != nil {
		visit(action.update.from)
		visit(&projectNode{expressions: action.update.assignments})
		if action.update.predicate != nil {
			visit(&projectNode{expressions: []expr.Expr{action.update.predicate}})
		}
	}
	visit(action.deleteUsing)
	for _, row := range action.expressions {
		for _, bound := range row {
			visit(&projectNode{expressions: []expr.Expr{bound}})
		}
	}
	for _, bound := range action.defaults {
		visit(&projectNode{expressions: []expr.Expr{bound}})
	}
	if action.returning != nil {
		visit(&projectNode{expressions: action.returning.expressions})
	}
	if action.predicate != nil {
		visit(&projectNode{expressions: []expr.Expr{action.predicate}})
	}
	if action.conflict != nil {
		if action.conflict.predicate != nil {
			visit(&projectNode{expressions: []expr.Expr{action.conflict.predicate}})
		}
		if action.conflict.update != nil {
			visit(&projectNode{expressions: action.conflict.update.assignments})
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
