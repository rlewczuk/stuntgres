package engine

import "stuntgres/internal/engine/expr"

// pruneProjectWindows retains analyzed slots and original routine references,
// but removes execution demand for window occurrences absent from the surviving
// expressions above their stage. Empty input is not evidence of absent demand.
func pruneProjectWindows(project *projectNode) (*projectNode, error) {
	var window *windowNode
	for node := project.Child; node != nil; {
		if candidate, ok := node.(*windowNode); ok {
			window = candidate
			break
		}
		switch node.(type) {
		case *projectNode, *derivedNode:
			return project, nil
		}
		left, right := queryInputs(node)
		if right != nil {
			return project, nil
		}
		node = left
	}
	if window == nil || len(window.Calls) == 0 {
		return project, nil
	}
	var small [16]bool
	used := small[:]
	if len(window.Calls) > len(used) {
		used = make([]bool, len(window.Calls))
	}
	used = used[:len(window.Calls)]
	// Rule grafts can expand the input and retag consumer frames without
	// changing the original call expression's analyzed identity. Demand reads
	// the current physical overlay, not those retained metadata coordinates.
	firstSlot := len(window.Child.Schema())
	var expression func(expr.Expr)
	var query func(LogicalNode)
	query = func(node LogicalNode) {
		if node == nil {
			return
		}
		queryExpressions(node, expression)
		left, right := queryInputs(node)
		query(left)
		query(right)
	}
	expression = func(bound expr.Expr) {
		// ORDER/LIMIT lowering can own the original CASE in a sort or
		// evaluation slot while projection only reads its cached result.
		// Use the existing physical-literal demand policy before those
		// owners preflight; a lexical walk would revive their dead arms.
		walkDemandedBoundExpression(bound, func(expr expr.Expr) {
			if occurrence, ok := expr.(*windowExpression); ok {
				// Physical window results occupy consecutive slots after input.
				i := occurrence.Slot - firstSlot
				if i >= 0 && i < len(window.Calls) && occurrence.Frame == project.Frame {
					used[i] = true
				}
			}
			if subquery, ok := expr.(subqueryExpression); ok && !subquery.cteDefinition && subquery.QueryPlan != nil {
				query(subquery.QueryPlan.Root)
			}
		})
	}
	for _, expr := range project.Expressions {
		expression(expr)
	}
	for node := project.Child; node != window; {
		if !queryIgnoresOwnExpressions(node) {
			queryExpressions(node, expression)
		}
		node, _ = queryInputs(node)
	}
	var calls []windowCall
	for i, call := range window.Calls {
		if !used[i] && !call.AnalysisOnly {
			if calls == nil {
				calls = append([]windowCall(nil), window.Calls...)
			}
			calls[i].AnalysisOnly = true
		}
	}
	if calls == nil {
		return project, nil
	}
	copy := *window
	copy.Calls = calls
	child, err := rewriteQueryExpressions(project.Child, func(expr expr.Expr) (expr.Expr, bool, error) {
		return expr, false, nil
	}, func(node LogicalNode) (LogicalNode, error) {
		if node == window {
			return &copy, nil
		}
		return node, nil
	})
	if err != nil {
		return nil, err
	}
	result := *project
	result.Child = child
	return &result, nil
}
