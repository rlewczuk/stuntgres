package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

// Copy only paths changed by an expression rewrite. Query-local frame identities
// and analyzed schemas are preserved across outer grouping and stored replay.
func rewriteQueryExpressions(node LogicalNode, rewrite func(expr.Expr) (expr.Expr, bool, error), rewriteNode ...func(LogicalNode) (LogicalNode, error)) (LogicalNode, error) {
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
	expression := func(old expr.Expr) expr.Expr {
		if old == nil || err != nil {
			return old
		}
		next, different, failure := rewrite(old)
		err = failure
		changed = changed || different
		return next
	}
	args := func(old []expr.Expr) []expr.Expr {
		var next []expr.Expr
		for i, item := range old {
			if err != nil {
				break
			}
			replaced, different, failure := rewrite(item)
			err = failure
			if different {
				if next == nil {
					next = append([]expr.Expr(nil), old...)
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
	stringCalls := func(old []expr.Expr) []expr.Expr {
		var next []expr.Expr
		for i, call := range old {
			original := call.(expr.Composite).Children()
			arguments := args(original)
			if len(arguments) == 0 || &arguments[0] == &original[0] {
				continue
			}
			if next == nil {
				next = append([]expr.Expr(nil), old...)
			}
			if err == nil {
				next[i], err = expr.Substitute(call.(expr.Composite), expr.InfoOf(call), arguments)
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
		calls := stringCalls(n.Calls)
		if changed && err == nil {
			copy := *n
			copy.Calls = calls
			out = &copy
		}
	case *setProjectNode:
		input := child(n.Child)
		calls := stringCalls(n.Calls)
		scalars := args(n.Scalars)
		template := n.Template
		if template != nil {
			expressions, origins, scalars := args(template.Expressions), args(template.Origins), args(template.Scalars)
			if len(expressions) != 0 && &expressions[0] != &template.Expressions[0] ||
				len(origins) != 0 && &origins[0] != &template.Origins[0] ||
				len(scalars) != 0 && &scalars[0] != &template.Scalars[0] {
				copy := *template
				copy.Expressions, copy.Origins, copy.Scalars = expressions, origins, scalars
				template = &copy
			}
		}
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Calls, copy.Scalars = input, calls, scalars
			copy.Template = template
			out = &copy
		}
	case *setNode:
		left, right := child(n.Left), child(n.Right)
		leftExpressions, rightExpressions := args(n.LeftExpressions), args(n.RightExpressions)
		if changed && err == nil {
			copy := *n
			copy.Left, copy.Right = left.(*projectNode), right.(*projectNode)
			copy.LeftExpressions, copy.RightExpressions = leftExpressions, rightExpressions
			out = &copy
		}
	case *recursiveUnionNode:
		left, right := child(n.Left), child(n.Right)
		leftExpressions, rightExpressions := args(n.LeftExpressions), args(n.RightExpressions)
		cycle := n.Cycle
		if cycle != nil {
			marks := args([]expr.Expr{cycle.Mark, cycle.Fallback})
			if marks[0] != cycle.Mark || marks[1] != cycle.Fallback {
				copy := *cycle
				copy.Mark, copy.Fallback = marks[0], marks[1]
				cycle = &copy
			}
		}
		if changed && err == nil {
			copy := *n
			copy.Left, copy.Right = left.(*projectNode), right.(*projectNode)
			copy.LeftExpressions, copy.RightExpressions = leftExpressions, rightExpressions
			copy.Cycle = cycle
			out = &copy
		}
	case *worktableScanNode:
		return node, nil
	case *sourceNode, *singletonNode:
		return node, nil
	case *projectNode:
		input, expressions := child(n.Child), args(n.Expressions)
		analysis := args(n.AnalysisExpressions)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Expressions, copy.AnalysisExpressions = input, expressions, analysis
			out = &copy
		}
	case *distinctNode:
		input, expressions := child(n.Child), args(n.Expressions)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Expressions = input, expressions
			out = &copy
		}
	case *distinctOnNode:
		input, expressions := child(n.Child), args(n.Expressions)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Expressions = input, expressions
			out = &copy
		}
	case *filterNode:
		input, predicate := child(n.Child), expression(n.Predicate)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Predicate = input, predicate
			out = &copy
		}
	case *rowLockNode:
		input := child(n.Child)
		if changed && err == nil {
			copy := *n
			copy.Child = input
			out = &copy
		}
	case *limitNode:
		input := child(n.Child)
		offset, count := expression(n.Offset), expression(n.Count)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Offset, copy.Count = input, offset, count
			out = &copy
		}
	case *evaluationNode:
		input, expressions := child(n.Child), args(n.Expressions)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Expressions = input, expressions
			out = &copy
		}
	case *joinNode:
		left, right := child(n.Left), child(n.Right)
		predicate, merges := expression(n.Predicate), args(n.Merges)
		if changed && err == nil {
			copy := *n
			copy.Left, copy.Right, copy.Predicate, copy.Merges = left, right, predicate, merges
			out = &copy
		}
	case *derivedNode:
		if n.Query == nil {
			break
		}
		root := child(n.Query.Root)
		if root != n.Query.Root && err == nil {
			copy := *n
			query := *n.Query
			query.Root = root
			copy.Query = &query
			out = &copy
		}
	case *sortNode:
		input, outputs := child(n.Child), args(n.Outputs)
		var keys []sortKey
		for i, key := range n.Keys {
			if err != nil {
				break
			}
			next, different, failure := rewrite(key.Expression)
			err = failure
			if different {
				if keys == nil {
					keys = append([]sortKey(nil), n.Keys...)
				}
				keys[i].Expression = next
				changed = true
			}
		}
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Outputs = input, outputs
			if keys != nil {
				copy.Keys = keys
			}
			out = &copy
		}
	case *windowNode:
		input := child(n.Child)
		var calls []windowCall
		for i, call := range n.Calls {
			if call.AnalysisOnly {
				continue
			}
			for j, expression := range [3]expr.Expr{call.Argument, call.Ordinal, call.DefaultValue} {
				if expression == nil || err != nil {
					continue
				}
				next, different, failure := rewrite(expression)
				err = failure
				if different {
					if calls == nil {
						calls = append([]windowCall(nil), n.Calls...)
					}
					if j == 0 {
						calls[i].Argument = next
					} else if j == 1 {
						calls[i].Ordinal = next
					} else {
						calls[i].DefaultValue = next
					}
					changed = true
				}
			}
		}
		var specs []boundWindowSpec
		for i, spec := range n.Specs {
			if !n.SpecActive(i) {
				continue
			}
			partition := args(spec.Partition)
			specChanged := len(partition) > 0 && &partition[0] != &spec.Partition[0]
			var order []sortKey
			for j, key := range spec.Order {
				if err != nil {
					break
				}
				next, different, failure := rewrite(key.Expression)
				err = failure
				if different {
					if order == nil {
						order = append([]sortKey(nil), spec.Order...)
					}
					order[j].Expression = next
					specChanged = true
					changed = true
				}
			}
			frame := spec.Frame
			for _, bound := range []*boundWindowBound{&frame.Start, &frame.End} {
				if bound.Offset == nil || err != nil {
					continue
				}
				next, different, failure := rewrite(bound.Offset)
				err = failure
				if different {
					bound.Offset = next
					specChanged = true
					changed = true
				}
			}
			if specChanged {
				if specs == nil {
					specs = append([]boundWindowSpec(nil), n.Specs...)
				}
				specs[i].Partition = partition
				specs[i].Frame = frame
				if order != nil {
					specs[i].Order = order
				}
			}
		}
		if changed && err == nil {
			copy := *n
			copy.Child = input
			if calls != nil {
				copy.Calls = calls
			}
			if specs != nil {
				copy.Specs = specs
			}
			out = &copy
		}
	case *aggregateNode:
		input, keys := child(n.Child), args(n.Keys)
		var calls []aggregateCall
		for i, call := range n.Calls {
			if err != nil {
				break
			}
			nextCall := call
			callChanged := false
			for j, expr := range [3]expr.Expr{call.Argument, call.Separator, call.Filter} {
				if expr == nil || err != nil {
					continue
				}
				next, different, failure := rewrite(expr)
				err = failure
				if different {
					callChanged = true
					if j == 0 {
						nextCall.Argument = next
					} else if j == 1 {
						nextCall.Separator = next
					} else {
						nextCall.Filter = next
					}
				}
			}
			var direct []expr.Expr
			for j, operand := range call.Direct {
				if err != nil {
					break
				}
				next, different, failure := rewrite(operand)
				err = failure
				if different {
					if direct == nil {
						direct = append([]expr.Expr(nil), call.Direct...)
					}
					direct[j] = next
					callChanged = true
				}
			}
			if direct != nil {
				nextCall.Direct = direct
			}
			var order []sortKey
			for k, key := range call.Order {
				if err != nil {
					break
				}
				next, different, failure := rewrite(key.Expression)
				err = failure
				if different {
					if order == nil {
						order = append([]sortKey(nil), call.Order...)
					}
					order[k].Expression = next
					callChanged = true
				}
			}
			if callChanged {
				if order != nil {
					nextCall.Order = order
				}
				if calls == nil {
					calls = append([]aggregateCall(nil), n.Calls...)
				}
				calls[i] = nextCall
				changed = true
			}
		}
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Keys = input, keys
			if calls != nil {
				copy.Calls = calls
			}
			out = &copy
		}
	case *valuesNode:
		var rows [][]expr.Expr
		for i, row := range n.Rows {
			next := args(row)
			if changed && rows == nil {
				rows = append([][]expr.Expr(nil), n.Rows...)
			}
			if rows != nil {
				rows[i] = next
			}
		}
		if rows != nil {
			copy := *n
			copy.Rows = rows
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
	queryExpressions(node, func(expr expr.Expr) {
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
func expressionUsesFrame(bound expr.Expr, frame uint32) bool {
	found := false
	walkBoundExpression(bound, func(expr expr.Expr) {
		switch e := expr.(type) {
		case columnExpression:
			found = found || e.Frame == frame
		case returningWholeRowExpression:
			found = found || e.Frame == frame
		case mergeActionExpression:
			found = found || e.Frame == frame
		case aggregateExpression:
			found = found || e.Frame == frame
		case groupingExpression:
			found = found || e.Frame == frame
		case *windowExpression:
			found = found || e.Frame == frame
		case groupExpression:
			found = found || e.Frame == frame
		case subqueryExpression:
			found = found || queryUsesFrame(e.QueryPlan.Root, frame)
		}
	})
	return found
}
