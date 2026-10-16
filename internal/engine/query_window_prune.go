package engine

// pruneProjectWindows retains analyzed slots and original routine references,
// but removes execution demand for window occurrences absent from the surviving
// expressions above their stage. Empty input is not evidence of absent demand.
func pruneProjectWindows(project *projectNode) (*projectNode, error) {
	var window *windowNode
	for node := project.child; node != nil; {
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
	if window == nil || len(window.calls) == 0 {
		return project, nil
	}
	var small [16]bool
	used := small[:]
	if len(window.calls) > len(used) {
		used = make([]bool, len(window.calls))
	}
	used = used[:len(window.calls)]
	// Rule grafts can expand the input and retag consumer frames without
	// changing the original call expression's analyzed identity. Demand reads
	// the current physical overlay, not those retained metadata coordinates.
	firstSlot := len(window.child.Schema())
	var expression func(BoundExpr)
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
	expression = func(expr BoundExpr) {
		// ORDER/LIMIT lowering can own the original CASE in a sort or
		// evaluation slot while projection only reads its cached result.
		// Use the existing physical-literal demand policy before those
		// owners preflight; a lexical walk would revive their dead arms.
		walkDemandedBoundExpression(expr, func(expr BoundExpr) {
			if occurrence, ok := expr.(*windowExpression); ok {
				// Physical window results occupy consecutive slots after input.
				i := occurrence.slot - firstSlot
				if i >= 0 && i < len(window.calls) && occurrence.frame == project.frame {
					used[i] = true
				}
			}
			if subquery, ok := expr.(subqueryExpression); ok && !subquery.cteDefinition && subquery.queryPlan != nil {
				query(subquery.queryPlan.Root)
			}
		})
	}
	for _, expr := range project.expressions {
		expression(expr)
	}
	for node := project.child; node != window; {
		if !queryIgnoresOwnExpressions(node) {
			queryExpressions(node, expression)
		}
		node, _ = queryInputs(node)
	}
	var calls []windowCall
	for i, call := range window.calls {
		if !used[i] && !call.analysisOnly {
			if calls == nil {
				calls = append([]windowCall(nil), window.calls...)
			}
			calls[i].analysisOnly = true
		}
	}
	if calls == nil {
		return project, nil
	}
	copy := *window
	copy.calls = calls
	child, err := rewriteQueryExpressions(project.child, func(expr BoundExpr) (BoundExpr, bool, error) {
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
	result.child = child
	return &result, nil
}

func (n *windowNode) specActive(index int) bool {
	for i, call := range n.calls {
		if !call.analysisOnly && n.specIndices[i] == index {
			return true
		}
	}
	return false
}
