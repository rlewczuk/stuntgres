package engine

import (
	"slices"

	"stuntgres/internal/engine/expr"
)

func ruleQueryHasPseudo(root LogicalNode) bool {
	found := false
	var visit func(LogicalNode)
	visit = func(node LogicalNode) {
		if node == nil || found {
			return
		}
		queryExpressions(node, func(bound expr.Expr) {
			walkBoundExpression(bound, func(expr expr.Expr) {
				if column, ok := expr.(columnExpression); ok && column.Scope == rulePseudoScope {
					found = true
				}
				if query, ok := expr.(subqueryExpression); ok {
					visit(query.QueryPlan.Root)
				}
			})
		})
		left, right := queryInputs(node)
		visit(left)
		visit(right)
	}
	visit(root)
	return found
}

// Transparent stages preserve the expanded input layout; aggregate and final
// project stages retain their independently analyzed output layouts. Window
// slots and direct ORDER keys follow the changed input width, not rule counts.
func ruleGraftSchemas(root, input, replacement LogicalNode, windowSlots map[int]int) (LogicalNode, error) {
	if root == input {
		return replacement, nil
	}
	switch n := root.(type) {
	case *projectNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		return &copy, nil
	case *filterNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.querySchema = querySchema(child.Schema())
		return &copy, nil
	case *sortNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.querySchema = querySchema(child.Schema())
		return &copy, nil
	case *limitNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.querySchema = querySchema(child.Schema())
		return &copy, nil
	case *rowLockNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.querySchema = querySchema(child.Schema())
		return &copy, nil
	case *evaluationNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.querySchema = querySchema(child.Schema())
		return &copy, nil
	case *aggregateNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		return &copy, nil
	case *windowNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		oldWidth := len(n.Child.Schema())
		delta := len(child.Schema()) - oldWidth
		copy.querySchema = append(slices.Clone(child.Schema()), n.Schema()[oldWidth:]...)
		for slot := oldWidth; slot < len(n.Schema()); slot++ {
			windowSlots[slot] = slot + delta
		}
		return &copy, nil
	case *setProjectNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		oldWidth := len(n.Child.Schema())
		copy.querySchema = append(slices.Clone(child.Schema()), n.Schema()[oldWidth:]...)
		copy.BaseWidth += len(child.Schema()) - oldWidth
		return &copy, nil
	}
	return root, nil
}
