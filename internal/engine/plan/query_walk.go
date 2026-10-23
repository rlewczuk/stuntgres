package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

// Copy only paths changed by an expression rewrite. Query-local frame identities
// and analyzed schemas are preserved across outer grouping and stored replay.
func RewriteQueryExpressions(node Node, rewrite func(expr.Expr) (expr.Expr, bool, error), rewriteNode ...func(Node) (Node, error)) (Node, error) {
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
	child := func(old Node) Node {
		if err != nil {
			return old
		}
		next, failure := RewriteQueryExpressions(old, rewrite, rewriteNode...)
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
	var out Node
	switch n := node.(type) {
	case *FunctionFromNode:
		calls := stringCalls(n.Calls)
		if changed && err == nil {
			copy := *n
			copy.Calls = calls
			out = &copy
		}
	case *SetProjectNode:
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
	case *SetNode:
		left, right := child(n.Left), child(n.Right)
		leftExpressions, rightExpressions := args(n.LeftExpressions), args(n.RightExpressions)
		if changed && err == nil {
			copy := *n
			copy.Left, copy.Right = left.(*ProjectNode), right.(*ProjectNode)
			copy.LeftExpressions, copy.RightExpressions = leftExpressions, rightExpressions
			out = &copy
		}
	case *RecursiveUnionNode:
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
			copy.Left, copy.Right = left.(*ProjectNode), right.(*ProjectNode)
			copy.LeftExpressions, copy.RightExpressions = leftExpressions, rightExpressions
			copy.Cycle = cycle
			out = &copy
		}
	case *WorktableScanNode:
		return node, nil
	case *SourceNode, *SingletonNode:
		return node, nil
	case *ProjectNode:
		input, expressions := child(n.Child), args(n.Expressions)
		analysis := args(n.AnalysisExpressions)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Expressions, copy.AnalysisExpressions = input, expressions, analysis
			out = &copy
		}
	case *DistinctNode:
		input, expressions := child(n.Child), args(n.Expressions)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Expressions = input, expressions
			out = &copy
		}
	case *DistinctOnNode:
		input, expressions := child(n.Child), args(n.Expressions)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Expressions = input, expressions
			out = &copy
		}
	case *FilterNode:
		input, predicate := child(n.Child), expression(n.Predicate)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Predicate = input, predicate
			out = &copy
		}
	case *RowLockNode:
		input := child(n.Child)
		if changed && err == nil {
			copy := *n
			copy.Child = input
			out = &copy
		}
	case *LimitNode:
		input := child(n.Child)
		offset, count := expression(n.Offset), expression(n.Count)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Offset, copy.Count = input, offset, count
			out = &copy
		}
	case *EvaluationNode:
		input, expressions := child(n.Child), args(n.Expressions)
		if changed && err == nil {
			copy := *n
			copy.Child, copy.Expressions = input, expressions
			out = &copy
		}
	case *JoinNode:
		left, right := child(n.Left), child(n.Right)
		predicate, merges := expression(n.Predicate), args(n.Merges)
		if changed && err == nil {
			copy := *n
			copy.Left, copy.Right, copy.Predicate, copy.Merges = left, right, predicate, merges
			out = &copy
		}
	case *DerivedNode:
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
	case *SortNode:
		input, outputs := child(n.Child), args(n.Outputs)
		var keys []SortKey
		for i, key := range n.Keys {
			if err != nil {
				break
			}
			next, different, failure := rewrite(key.Expression)
			err = failure
			if different {
				if keys == nil {
					keys = append([]SortKey(nil), n.Keys...)
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
	case *WindowNode:
		input := child(n.Child)
		var calls []WindowCall
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
						calls = append([]WindowCall(nil), n.Calls...)
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
		var specs []WindowSpec
		for i, spec := range n.Specs {
			if !n.SpecActive(i) {
				continue
			}
			partition := args(spec.Partition)
			specChanged := len(partition) > 0 && &partition[0] != &spec.Partition[0]
			var order []SortKey
			for j, key := range spec.Order {
				if err != nil {
					break
				}
				next, different, failure := rewrite(key.Expression)
				err = failure
				if different {
					if order == nil {
						order = append([]SortKey(nil), spec.Order...)
					}
					order[j].Expression = next
					specChanged = true
					changed = true
				}
			}
			frame := spec.Frame
			for _, bound := range []*WindowBound{&frame.Start, &frame.End} {
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
					specs = append([]WindowSpec(nil), n.Specs...)
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
	case *AggregateNode:
		input, keys := child(n.Child), args(n.Keys)
		var calls []AggregateCall
		for i, call := range n.Calls {
			if err != nil {
				break
			}
			nextCall := call
			callChanged := false
			for j := 0; j <= len(call.Arguments); j++ {
				operand := call.Filter
				if j < len(call.Arguments) {
					operand = call.Arguments[j]
				}
				if operand == nil || err != nil {
					continue
				}
				next, different, failure := rewrite(operand)
				err = failure
				if different {
					if j < len(call.Arguments) {
						if !callChanged {
							nextCall.Arguments = append([]expr.Expr(nil), call.Arguments...)
						}
						nextCall.Arguments[j] = next
					} else {
						nextCall.Filter = next
					}
					callChanged = true
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
			var order []SortKey
			for k, key := range call.Order {
				if err != nil {
					break
				}
				next, different, failure := rewrite(key.Expression)
				err = failure
				if different {
					if order == nil {
						order = append([]SortKey(nil), call.Order...)
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
					calls = append([]AggregateCall(nil), n.Calls...)
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
	case *ValuesNode:
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

// Runtime demand omits unused definitions, while PostgreSQL's static folding
// eligibility inspects even nested WITH bodies that will never execute.
func VisitExecutableQuery(node Node, visit func(Node), expression func(expr.Expr)) {
	VisitCTEQuery(node, visit, expression, false)
}

func VisitCTEQuery(node Node, visit func(Node), expression func(expr.Expr), includeDefinitions bool) {
	if node == nil {
		return
	}
	visit(node)
	QueryExpressions(node, func(bound expr.Expr) {
		if bound == nil {
			return
		}
		if edge, ok := bound.(SubqueryExpr); ok && edge.CTEEdge && !includeDefinitions {
			return
		}
		expression(bound)
		WalkExpression(bound, func(n expr.Expr) {
			if s, ok := n.(SubqueryExpr); ok {
				VisitCTEQuery(s.QueryPlan.Root, visit, expression, includeDefinitions)
			}
		})
	})
	left, right := QueryInputs(node)
	VisitCTEQuery(left, visit, expression, includeDefinitions)
	VisitCTEQuery(right, visit, expression, includeDefinitions)
}

// Logical inputs exclude expression-owned queries. Consumers visit those at
// explicit subquery boundaries, preserving lexical frames and leaf occurrences.
func QueryInputs(node Node) (Node, Node) {
	switch n := node.(type) {
	case *SetProjectNode:
		return n.Child, nil
	case *SetNode:
		return n.Left, n.Right
	case *RecursiveUnionNode:
		return n.Left, n.Right
	case *ProjectNode:
		return n.Child, nil
	case *FilterNode:
		return n.Child, nil
	case *SortNode:
		return n.Child, nil
	case *WindowNode:
		return n.Child, nil
	case *AggregateNode:
		return n.Child, nil
	case *DistinctNode:
		return n.Child, nil
	case *DistinctOnNode:
		return n.Child, nil
	case *LimitNode:
		return n.Child, nil
	case *RowLockNode:
		return n.Child, nil
	case *EvaluationNode:
		return n.Child, nil
	case *JoinNode:
		return n.Left, n.Right
	case *DerivedNode:
		if n.Query != nil {
			return n.Query.Root, nil
		}
	}
	return nil, nil
}

func QueryExpressions(node Node, visit func(expr.Expr)) {
	args := func(items []expr.Expr) {
		for _, expr := range items {
			visit(expr)
		}
	}
	switch n := node.(type) {
	case *FunctionFromNode:
		for _, call := range n.Calls {
			visit(call)
		}
	case *SetProjectNode:
		for _, call := range n.Calls {
			visit(call)
		}
		args(n.Scalars)
		if n.Template != nil {
			args(n.Template.Expressions)
			args(n.Template.Origins)
			args(n.Template.Scalars)
		}
	case *SetNode:
		args(n.LeftExpressions)
		args(n.RightExpressions)
	case *RecursiveUnionNode:
		args(n.LeftExpressions)
		args(n.RightExpressions)
		if c := n.Cycle; c != nil {
			visit(c.Mark)
			visit(c.Fallback)
		}
	case *ProjectNode:
		args(n.Expressions)
		args(n.AnalysisExpressions)
	case *FilterNode:
		visit(n.Predicate)
	case *SortNode:
		for _, key := range n.Keys {
			visit(key.Expression)
		}
		args(n.Outputs)
	case *WindowNode:
		for _, call := range n.Calls {
			if call.AnalysisOnly {
				continue
			}
			visit(call.Argument)
			visit(call.Ordinal)
			visit(call.DefaultValue)
		}
		for i, spec := range n.Specs {
			if !n.SpecActive(i) {
				continue
			}
			args(spec.Partition)
			for _, key := range spec.Order {
				visit(key.Expression)
			}
			visit(spec.Frame.Start.Offset)
			visit(spec.Frame.End.Offset)
		}
	case *AggregateNode:
		args(n.Keys)
		for _, call := range n.Calls {
			call.Expressions(visit)
		}
	case *DistinctNode:
		args(n.Expressions)
	case *DistinctOnNode:
		args(n.Expressions)
	case *LimitNode:
		visit(n.Offset)
		visit(n.Count)
	case *EvaluationNode:
		args(n.Expressions)
	case *JoinNode:
		visit(n.Predicate)
		args(n.Merges)
	case *ValuesNode:
		for _, row := range n.Rows {
			args(row)
		}
	}
}
