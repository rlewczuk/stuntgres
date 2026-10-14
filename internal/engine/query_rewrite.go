package engine

import "fmt"

// Copy only paths changed by an expression rewrite. Query-local frame identities
// and analyzed schemas are preserved across outer grouping and stored replay.
func rewriteQueryExpressions(node LogicalNode, rewrite func(BoundExpr) (BoundExpr, bool, error), rewriteNode ...func(LogicalNode) (LogicalNode, error)) (LogicalNode, error) {
	if node == nil {
		return nil, nil
	}
	if len(rewriteNode) != 0 && rewriteNode[0] != nil {
		next, err := rewriteNode[0](node)
		if err != nil || next != node {
			return next, err
		}
	}
	changed := false
	var err error
	child := func(old LogicalNode) LogicalNode {
		if err != nil {
			return old
		}
		next, failure := rewriteQueryExpressions(old, rewrite, rewriteNode...)
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
	// Stage-owned set calls cannot be replaced by outer GROUP keys or folded
	// into scalars. Their lexical arguments still use the ordinary rewrite.
	stringCalls := func(old []BoundExpr) []BoundExpr {
		var next []BoundExpr
		for i, call := range old {
			original := functionCallArguments(call)
			arguments := args(original)
			if len(arguments) == 0 || &arguments[0] == &original[0] {
				continue
			}
			if next == nil {
				next = append([]BoundExpr(nil), old...)
			}
			if err == nil {
				next[i], err = withFunctionCallArguments(call, arguments)
			}
		}
		if next == nil {
			return old
		}
		return next
	}
	var out LogicalNode
	switch n := node.(type) {
	case *functionFromNode:
		calls := stringCalls(n.calls)
		if changed && err == nil {
			copy := *n
			copy.calls = calls
			out = &copy
		}
	case *setProjectNode:
		input := child(n.child)
		calls := stringCalls(n.calls)
		scalars := args(n.scalars)
		template := n.template
		if template != nil {
			expressions, origins, scalars := args(template.expressions), args(template.origins), args(template.scalars)
			if len(expressions) != 0 && &expressions[0] != &template.expressions[0] ||
				len(origins) != 0 && &origins[0] != &template.origins[0] ||
				len(scalars) != 0 && &scalars[0] != &template.scalars[0] {
				copy := *template
				copy.expressions, copy.origins, copy.scalars = expressions, origins, scalars
				template = &copy
			}
		}
		if changed && err == nil {
			copy := *n
			copy.child, copy.calls, copy.scalars = input, calls, scalars
			copy.template = template
			out = &copy
		}
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
		analysis := args(n.analysisExpressions)
		if changed && err == nil {
			copy := *n
			copy.child, copy.expressions, copy.analysisExpressions = input, expressions, analysis
			out = &copy
		}
	case *distinctNode:
		input, expressions := child(n.child), args(n.expressions)
		if changed && err == nil {
			copy := *n
			copy.child, copy.expressions = input, expressions
			out = &copy
		}
	case *distinctOnNode:
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
		if n.query == nil {
			break
		}
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
	case *windowNode:
		input := child(n.child)
		var calls []windowCall
		for i, call := range n.calls {
			if call.argument == nil || err != nil {
				continue
			}
			next, different, failure := rewrite(call.argument)
			err = failure
			if different {
				if calls == nil {
					calls = append([]windowCall(nil), n.calls...)
				}
				calls[i].argument = next
				changed = true
			}
		}
		var specs []boundWindowSpec
		for i, spec := range n.specs {
			partition := args(spec.partition)
			specChanged := len(partition) > 0 && &partition[0] != &spec.partition[0]
			var order []sortKey
			for j, key := range spec.order {
				if err != nil {
					break
				}
				next, different, failure := rewrite(key.expression)
				err = failure
				if different {
					if order == nil {
						order = append([]sortKey(nil), spec.order...)
					}
					order[j].expression = next
					specChanged = true
					changed = true
				}
			}
			frame := spec.frame
			for _, bound := range []*boundWindowBound{&frame.start, &frame.end} {
				if bound.offset == nil || err != nil {
					continue
				}
				next, different, failure := rewrite(bound.offset)
				err = failure
				if different {
					bound.offset = next
					specChanged = true
					changed = true
				}
			}
			if specChanged {
				if specs == nil {
					specs = append([]boundWindowSpec(nil), n.specs...)
				}
				specs[i].partition = partition
				specs[i].frame = frame
				if order != nil {
					specs[i].order = order
				}
			}
		}
		if changed && err == nil {
			copy := *n
			copy.child = input
			if calls != nil {
				copy.calls = calls
			}
			if specs != nil {
				copy.specs = specs
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
			for j, expr := range [4]BoundExpr{call.argument, call.separator, call.filter, call.direct} {
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
					} else if j==2 {
						nextCall.filter = next
					} else {
						nextCall.direct=next
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
		case returningWholeRowExpression:
			found = found || e.frame == frame
		case mergeActionExpression:
			found = found || e.frame == frame
		case aggregateExpression:
			found = found || e.frame == frame
		case groupingExpression:
			found = found || e.frame == frame
		case *windowExpression:
			found = found || e.frame == frame
		case groupExpression:
			found = found || e.frame == frame
		case subqueryExpression:
			found = found || queryUsesFrame(e.queryPlan.Root, frame)
		}
	})
	return found
}
