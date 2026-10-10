package engine

import "fmt"

// Copy only paths changed by an expression rewrite. Query-local frame identities
// and analyzed schemas are preserved across outer grouping and stored replay.
func rewriteQueryExpressions(node LogicalNode, rewrite func(BoundExpr) (BoundExpr, bool, error)) (LogicalNode, error) {
	if node == nil {
		return nil, nil
	}
	changed := false
	var err error
	child := func(old LogicalNode) LogicalNode {
		if err != nil {
			return old
		}
		next, failure := rewriteQueryExpressions(old, rewrite)
		err = failure
		changed = changed || next != old
		return next
	}
	expression := func(old BoundExpr) BoundExpr {
		if old == nil || err != nil {
			return old
		}
		next, different, failure := rewrite(old)
		err = failure
		changed = changed || different
		return next
	}
	args := func(old []BoundExpr) []BoundExpr {
		var next []BoundExpr
		for i, item := range old {
			if err != nil {
				break
			}
			replaced, different, failure := rewrite(item)
			err = failure
			if different {
				if next == nil {
					next = append([]BoundExpr(nil), old...)
				}
				next[i] = replaced
				changed = true
			}
		}
		if next == nil {
			return old
		}
		return next
	}
	var out LogicalNode
	switch n := node.(type) {
	case *setNode:
		left, right := child(n.left), child(n.right)
		leftExpressions, rightExpressions := args(n.leftExpressions), args(n.rightExpressions)
		if changed && err == nil {
			copy := *n
			copy.left, copy.right = left.(*projectNode), right.(*projectNode)
			copy.leftExpressions, copy.rightExpressions = leftExpressions, rightExpressions
			out = &copy
		}
	case *sourceNode, *singletonNode:
		return node, nil
	case *projectNode:
		input, expressions := child(n.child), args(n.expressions)
		if changed && err == nil {
			copy := *n
			copy.child, copy.expressions = input, expressions
			out = &copy
		}
	case *distinctNode:
		input, expressions := child(n.child), args(n.expressions)
		if changed && err == nil {
			copy := *n
			copy.child, copy.expressions = input, expressions
			out = &copy
		}
	case *filterNode:
		input, predicate := child(n.child), expression(n.predicate)
		if changed && err == nil {
			copy := *n
			copy.child, copy.predicate = input, predicate
			out = &copy
		}
	case *rowLockNode:
		input := child(n.child)
		if changed && err == nil {
			copy := *n
			copy.child = input
			out = &copy
		}
	case *limitNode:
		input := child(n.child)
		offset, count := expression(n.offset), expression(n.count)
		if changed && err == nil {
			copy := *n
			copy.child, copy.offset, copy.count = input, offset, count
			out = &copy
		}
	case *evaluationNode:
		input, expressions := child(n.child), args(n.expressions)
		if changed && err == nil {
			copy := *n
			copy.child, copy.expressions = input, expressions
			out = &copy
		}
	case *joinNode:
		left, right := child(n.left), child(n.right)
		predicate, merges := expression(n.predicate), args(n.merges)
		if changed && err == nil {
			copy := *n
			copy.left, copy.right, copy.predicate, copy.merges = left, right, predicate, merges
			out = &copy
		}
	case *derivedNode:
		root := child(n.query.Root)
		if root != n.query.Root && err == nil {
			copy := *n
			query := *n.query
			query.Root = root
			copy.query = &query
			out = &copy
		}
	case *sortNode:
		input, outputs := child(n.child), args(n.outputs)
		var keys []sortKey
		for i, key := range n.keys {
			if err != nil {
				break
			}
			next, different, failure := rewrite(key.expression)
			err = failure
			if different {
				if keys == nil {
					keys = append([]sortKey(nil), n.keys...)
				}
				keys[i].expression = next
				changed = true
			}
		}
		if changed && err == nil {
			copy := *n
			copy.child, copy.outputs = input, outputs
			if keys != nil {
				copy.keys = keys
			}
			out = &copy
		}
	case *aggregateNode:
		input, keys := child(n.child), args(n.keys)
		var calls []aggregateCall
		for i, call := range n.calls {
			if err != nil {
				break
			}
			nextCall := call
			callChanged := false
			for j, expr := range [3]BoundExpr{call.argument, call.separator, call.filter} {
				if expr == nil || err != nil {
					continue
				}
				next, different, failure := rewrite(expr)
				err = failure
				if different {
					callChanged = true
					if j == 0 {
						nextCall.argument = next
					} else if j == 1 {
						nextCall.separator = next
					} else {
						nextCall.filter = next
					}
				}
			}
			var order []sortKey
			for k, key := range call.order {
				if err != nil {
					break
				}
				next, different, failure := rewrite(key.expression)
				err = failure
				if different {
					if order == nil {
						order = append([]sortKey(nil), call.order...)
					}
					order[k].expression = next
					callChanged = true
				}
			}
			if callChanged {
				if order != nil {
					nextCall.order = order
				}
				if calls == nil {
					calls = append([]aggregateCall(nil), n.calls...)
				}
				calls[i] = nextCall
				changed = true
			}
		}
		if changed && err == nil {
			copy := *n
			copy.child, copy.keys = input, keys
			if calls != nil {
				copy.calls = calls
			}
			out = &copy
		}
	case *valuesNode:
		var rows [][]BoundExpr
		for i, row := range n.rows {
			next := args(row)
			if changed && rows == nil {
				rows = append([][]BoundExpr(nil), n.rows...)
			}
			if rows != nil {
				rows[i] = next
			}
		}
		if rows != nil {
			copy := *n
			copy.rows = rows
			out = &copy
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid query expression rewrite node %T", node)
	}
	if err != nil {
		return nil, err
	}
	if !changed {
		return node, nil
	}
	return out, nil
}

func queryUsesFrame(node LogicalNode, frame uint32) bool {
	if node == nil {
		return false
	}
	found := false
	queryExpressions(node, func(expr BoundExpr) {
		if expressionUsesFrame(expr, frame) {
			found = true
		}
	})
	if found {
		return true
	}
	left, right := queryInputs(node)
	return queryUsesFrame(left, frame) || queryUsesFrame(right, frame)
}
func expressionUsesFrame(expr BoundExpr, frame uint32) bool {
	found := false
	walkBoundExpression(expr, func(expr BoundExpr) {
		switch e := expr.(type) {
		case columnExpression:
			found = found || e.frame == frame
		case aggregateExpression:
			found = found || e.frame == frame
		case groupExpression:
			found = found || e.frame == frame
		case subqueryExpression:
			found = found || queryUsesFrame(e.queryPlan.Root, frame)
		}
	})
	return found
}
