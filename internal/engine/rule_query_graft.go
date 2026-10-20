package engine

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func ruleQueryHasPseudo(root plan.Node) bool {
	found := false
	var visit func(plan.Node)
	visit = func(node plan.Node) {
		if node == nil || found {
			return
		}
		plan.QueryExpressions(node, func(bound expr.Expr) {
			plan.WalkExpression(bound, func(expr expr.Expr) {
				if column, ok := expr.(plan.ColumnExpr); ok && column.Scope == analyze.RulePseudoScope {
					found = true
				}
				if query, ok := expr.(plan.SubqueryExpr); ok {
					visit(query.QueryPlan.Root)
				}
			})
		})
		left, right := plan.QueryInputs(node)
		visit(left)
		visit(right)
	}
	visit(root)
	return found
}

// Transparent stages preserve the expanded input layout; aggregate and final
// project stages retain their independently analyzed output layouts. Window
// slots and direct ORDER keys follow the changed input width, not rule counts.
func ruleGraftSchemas(root, input, replacement plan.Node, windowSlots map[int]int) (plan.Node, error) {
	if root == input {
		return replacement, nil
	}
	switch n := root.(type) {
	case *plan.ProjectNode:
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
	case *plan.FilterNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.QuerySchema = plan.QuerySchema(child.Schema())
		return &copy, nil
	case *plan.SortNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.QuerySchema = plan.QuerySchema(child.Schema())
		return &copy, nil
	case *plan.LimitNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.QuerySchema = plan.QuerySchema(child.Schema())
		return &copy, nil
	case *plan.RowLockNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.QuerySchema = plan.QuerySchema(child.Schema())
		return &copy, nil
	case *plan.EvaluationNode:
		child, err := ruleGraftSchemas(n.Child, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return root, nil
		}
		copy := *n
		copy.Child = child
		copy.QuerySchema = plan.QuerySchema(child.Schema())
		return &copy, nil
	case *plan.AggregateNode:
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
	case *plan.WindowNode:
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
		copy.QuerySchema = append(slices.Clone(child.Schema()), n.Schema()[oldWidth:]...)
		for slot := oldWidth; slot < len(n.Schema()); slot++ {
			windowSlots[slot] = slot + delta
		}
		return &copy, nil
	case *plan.SetProjectNode:
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
		copy.QuerySchema = append(slices.Clone(child.Schema()), n.Schema()[oldWidth:]...)
		copy.BaseWidth += len(child.Schema()) - oldWidth
		return &copy, nil
	}
	return root, nil
}
