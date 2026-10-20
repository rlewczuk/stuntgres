package engine

import (
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// preflightQuery retains schemas and source identity from analysis. Only paths
// containing folded coercions are copied for this execution.
func preflightQuery(c *EvalContext, node plan.Node) (plan.Node, error) {
	if err := exec.EvaluationContext(c); err != nil {
		return nil, err
	}
	switch n := node.(type) {
	case *plan.FunctionFromNode:
		return preflightFunctionFrom(c, n)
	case *plan.SetProjectNode:
		return preflightQuerySet(c, n)
	case *plan.WindowNode:
		return plan.RewriteQueryExpressions(n, func(expr expr.Expr) (expr.Expr, bool, error) { return preflightCoercions(c, expr) }, func(child plan.Node) (plan.Node, error) {
			if child == n {
				return child, nil
			}
			return preflightQuery(c, child)
		})
	case *plan.SetNode:
		return preflightSet(c, n)
	case *plan.RecursiveUnionNode:
		bound, err := preflightSet(c, &n.SetNode)
		if err != nil {
			return nil, err
		}
		copy := *n
		copy.SetNode = *bound.(*plan.SetNode)
		if n.Cycle != nil {
			recipe := *n.Cycle
			recipe.Mark, _, err = preflightCoercions(c, recipe.Mark)
			if err != nil {
				return nil, err
			}
			recipe.Fallback, _, err = preflightCoercions(c, recipe.Fallback)
			if err != nil {
				return nil, err
			}
			copy.Cycle = &recipe
		}
		return &copy, nil
	case *plan.WorktableScanNode:
		return n, nil
	case *plan.DistinctNode:
		return preflightDistinct(c, n)
	case *plan.DistinctOnNode:
		return preflightDistinctOn(c, n)
	case *plan.DerivedNode:
		// Ordinary queries retain their runtime projection frontier. Rule
		// programs prepare only the child payload retained by producer demand.
		if !c.PlanStaticOperators || n.Query == nil {
			return n, nil
		}
		root, err := preflightQuery(c, n.Query.Root)
		if err != nil {
			return nil, err
		}
		if root == n.Query.Root {
			return n, nil
		}
		copy, query := *n, *n.Query
		query.Root = root
		copy.Query = &query
		return &copy, nil
	case *plan.LimitNode:
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		// Bounds retain their ordered runtime coercion and value-check frontier.
		// Folding COUNT here could overtake a failing OFFSET.
		if child != n.Child {
			copy := *n
			copy.Child = child
			return &copy, nil
		}
		return n, nil
	case *plan.EvaluationNode:
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		var expressions []expr.Expr
		for i, operand := range n.Expressions {
			folded, changed, err := preflightCoercions(c, operand)
			if err != nil {
				return nil, err
			}
			if owned, did := queryScalarOwner(child, folded); did {
				folded, changed = owned, true
			}
			if changed {
				if expressions == nil {
					expressions = append([]expr.Expr(nil), n.Expressions...)
				}
				expressions[i] = folded
			}
		}
		if child != n.Child || expressions != nil {
			copy := *n
			copy.Child = child
			if expressions != nil {
				copy.Expressions = expressions
			}
			return &copy, nil
		}
		return n, nil
	case *plan.RowLockNode:
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if child != n.Child {
			copy := *n
			copy.Child = child
			return &copy, nil
		}
		return n, nil
	case *plan.JoinNode:
		return preflightJoin(c, n)
	case *plan.SourceNode, *plan.SingletonNode:
		return n, nil
	case *plan.ValuesNode:
		var rows [][]expr.Expr
		for i, row := range n.Rows {
			var expressions []expr.Expr
			for j, expression := range row {
				folded, changed, err := preflightCoercions(c, expression)
				if err != nil {
					return nil, err
				}
				if changed {
					if expressions == nil {
						expressions = append([]expr.Expr(nil), row...)
					}
					expressions[j] = folded
				}
			}
			if expressions != nil {
				if rows == nil {
					rows = append([][]expr.Expr(nil), n.Rows...)
				}
				rows[i] = expressions
			}
		}
		if rows != nil {
			copy := *n
			copy.Rows = rows
			return &copy, nil
		}
	case *plan.AggregateNode:
		if n.PresenceOnly {
			child, err := preflightQuery(c, n.Child)
			if err != nil {
				return nil, err
			}
			if child == n.Child {
				return n, nil
			}
			copy := *n
			copy.Child = child
			return &copy, nil
		}
		var keys []expr.Expr
		for i, key := range n.Keys {
			folded, replacement, err := preflightCoercions(c, key)
			if err != nil {
				return nil, err
			}
			if replacement {
				if keys == nil {
					keys = append([]expr.Expr(nil), n.Keys...)
				}
				keys[i] = folded
			}
		}
		var calls []plan.AggregateCall
		for i, call := range n.Calls {
			// Ordered-set direct expressions remain group-finalization owned:
			// preflight must not evaluate them for absent keyed groups.
			for j, expression := range [3]expr.Expr{call.Argument, call.Separator, call.Filter} {
				if expression == nil {
					continue
				}
				folded, replacement, err := preflightCoercions(c, expression)
				if err != nil {
					return nil, err
				}
				if replacement {
					if calls == nil {
						calls = append([]plan.AggregateCall(nil), n.Calls...)
					}
					if j == 0 {
						calls[i].Argument = folded
					} else if j == 1 {
						calls[i].Separator = folded
					} else {
						calls[i].Filter = folded
					}
				}
			}
			var order []plan.SortKey
			for k, key := range call.Order {
				folded, replacement, err := preflightCoercions(c, key.Expression)
				if err != nil {
					return nil, err
				}
				if replacement {
					if order == nil {
						order = append([]plan.SortKey(nil), call.Order...)
					}
					order[k].Expression = folded
				}
			}
			if order != nil {
				if calls == nil {
					calls = append([]plan.AggregateCall(nil), n.Calls...)
				}
				calls[i].Order = order
			}
		}
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if keys != nil || calls != nil || child != n.Child {
			copy := *n
			copy.Child = child
			if keys != nil {
				copy.Keys = keys
			}
			if calls != nil {
				copy.Calls = calls
			}
			return &copy, nil
		}
	case *plan.FilterNode:
		predicate, changed, err := preflightCoercions(c, n.Predicate)
		if err != nil {
			return nil, err
		}
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if changed || child != n.Child {
			copy := *n
			copy.Child, copy.Predicate = child, predicate
			return &copy, nil
		}
	case *plan.SortNode:
		if n.PresenceOnly {
			child, err := preflightQuery(c, n.Child)
			if err != nil {
				return nil, err
			}
			if child == n.Child {
				return n, nil
			}
			copy := *n
			copy.Child = child
			return &copy, nil
		}
		var keys []plan.SortKey
		for i, key := range n.Keys {
			if key.Expression.CollationIndeterminate() {
				return nil, setOrderCollationError()
			}
			folded, changed, err := preflightCoercions(c, key.Expression)
			if err != nil {
				return nil, err
			}
			if changed {
				if keys == nil {
					keys = append([]plan.SortKey(nil), n.Keys...)
				}
				keys[i].Expression = folded
			}
		}
		var outputs []expr.Expr
		for i, operand := range n.Outputs {
			folded, changed, err := preflightCoercions(c, operand)
			if err != nil {
				return nil, err
			}
			if changed {
				if outputs == nil {
					outputs = append([]expr.Expr(nil), n.Outputs...)
				}
				outputs[i] = folded
			}
		}
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if child != n.Child || keys != nil || outputs != nil {
			copy := *n
			copy.Child = child
			if keys != nil {
				copy.Keys = keys
			}
			if outputs != nil {
				copy.Outputs = outputs
			}
			return &copy, nil
		}
	case *plan.ProjectNode:
		// Projection initialization precedes opening its child. CHECK recipes
		// may fail here, but checked conversion truth stays in each runtime row.
		var expressions []expr.Expr
		for i, expression := range n.Expressions {
			folded, changed, err := preflightCoercions(c, expression)
			if err != nil {
				return nil, err
			}
			if changed {
				if expressions == nil {
					expressions = append([]expr.Expr(nil), n.Expressions...)
				}
				expressions[i] = folded
			}
		}
		if expressions != nil {
			copy := *n
			copy.Expressions = expressions
			n, node = &copy, &copy
			expressions = nil
		}
		pruned, err := pruneProjectWindows(n)
		if err != nil {
			return nil, err
		}
		n, node = pruned, pruned
		if c.PlanStaticOperators {
			// Use surviving scalar recipes, not dead conditional branches, to
			// choose child payload demand. Scalar preparation keeps its usual
			// precedence over opening/preparing the source.
			demanded, err := ruleQueryProducerDemand(n, nil, 0)
			if err != nil {
				return nil, err
			}
			n, node = demanded.(*plan.ProjectNode), demanded
		}
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		for i, expression := range n.Expressions {
			if expressions != nil {
				expression = expressions[i]
			}
			if owned, changed := queryScalarOwner(child, expression); changed {
				if expressions == nil {
					expressions = append([]expr.Expr(nil), n.Expressions...)
				}
				expressions[i] = owned
			}
		}
		if expressions != nil || child != n.Child {
			copy := *n
			copy.Child = child
			if expressions != nil {
				copy.Expressions = expressions
			}
			return &copy, nil
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid logical query node")
	}
	return node, nil
}

func preflightJoin(c *EvalContext, n *plan.JoinNode) (plan.Node, error) {
	left, err := preflightQuery(c, n.Left)
	if err != nil {
		return nil, err
	}
	right, err := preflightQuery(c, n.Right)
	if err != nil {
		return nil, err
	}
	predicate := n.Predicate
	changed := false
	if predicate != nil {
		predicate, changed, err = preflightCoercions(c, predicate)
		if err != nil {
			return nil, err
		}
	}
	var merges []expr.Expr
	for i, expression := range n.Merges {
		folded, replacement, err := preflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		if replacement {
			if merges == nil {
				merges = append([]expr.Expr(nil), n.Merges...)
			}
			merges[i] = folded
		}
	}
	if changed || merges != nil || left != n.Left || right != n.Right {
		copy := *n
		copy.Left, copy.Right, copy.Predicate = left, right, predicate
		if merges != nil {
			copy.Merges = merges
		}
		return &copy, nil
	}
	return n, nil
}

func preflightSet(c *EvalContext, n *plan.SetNode) (plan.Node, error) {
	left, err := preflightQuery(c, n.Left)
	if err != nil {
		return nil, err
	}
	right, err := preflightQuery(c, n.Right)
	if err != nil {
		return nil, err
	}
	copy := *n
	copy.Left, copy.Right = left.(*plan.ProjectNode), right.(*plan.ProjectNode)
	changed := left != n.Left || right != n.Right
	for side, expressions := range [2][]expr.Expr{n.LeftExpressions, n.RightExpressions} {
		var next []expr.Expr
		for i, expression := range expressions {
			folded, different, err := preflightCoercions(c, expression)
			if err != nil {
				return nil, err
			}
			if different {
				if next == nil {
					next = append([]expr.Expr(nil), expressions...)
				}
				next[i] = folded
				changed = true
			}
		}
		if next != nil {
			if side == 0 {
				copy.LeftExpressions = next
			} else {
				copy.RightExpressions = next
			}
		}
	}
	if changed {
		return &copy, nil
	}
	return n, nil
}

func preflightDistinct(c *EvalContext, n *plan.DistinctNode) (plan.Node, error) {
	if n.PresenceOnly {
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if child == n.Child {
			return n, nil
		}
		copy := *n
		copy.Child = child
		return &copy, nil
	}
	child, err := preflightQuery(c, n.Child)
	if err != nil {
		return nil, err
	}
	var expressions []expr.Expr
	for i, expression := range n.Expressions {
		folded, changed, err := preflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		if owned, did := queryScalarOwner(child, folded); did {
			folded, changed = owned, true
		}
		if changed {
			if expressions == nil {
				expressions = append([]expr.Expr(nil), n.Expressions...)
			}
			expressions[i] = folded
		}
	}
	if child != n.Child || expressions != nil {
		copy := *n
		copy.Child = child
		if expressions != nil {
			copy.Expressions = expressions
		}
		return &copy, nil
	}
	return n, nil
}

func preflightDistinctOn(c *EvalContext, n *plan.DistinctOnNode) (plan.Node, error) {
	child, err := preflightQuery(c, n.Child)
	if err != nil {
		return nil, err
	}
	var expressions []expr.Expr
	if !n.PresenceOnly {
		for i, expression := range n.Expressions {
			folded, changed, err := preflightCoercions(c, expression)
			if err != nil {
				return nil, err
			}
			if changed {
				if expressions == nil {
					expressions = append([]expr.Expr(nil), n.Expressions...)
				}
				expressions[i] = folded
			}
		}
	}
	if child == n.Child && expressions == nil {
		return n, nil
	}
	copy := *n
	copy.Child = child
	if expressions != nil {
		copy.Expressions = expressions
	}
	return &copy, nil
}

func preflightFunctionFrom(c *EvalContext, n *plan.FunctionFromNode) (plan.Node, error) {
	// Plan every child before simplifying a source call. Keep the analyzed
	// call carrier: it owns aliases, record expansion and the fixed stage width.
	planned, err := plan.RewriteQueryExpressions(n, func(expr expr.Expr) (expr.Expr, bool, error) {
		return preflightCoercions(c, expr)
	})
	if err != nil {
		return nil, err
	}
	source := planned.(*plan.FunctionFromNode)
	var scalarNull []bool
	for i, call := range source.Calls {
		if !exec.StrictScalarFunctionSource(call) {
			continue
		}
		next, _, err := simplifyConditional(c, call, false)
		if err != nil {
			return nil, err
		}
		if exec.RandomLiteralNull(next) {
			if scalarNull == nil {
				scalarNull = make([]bool, len(source.Calls))
			}
			scalarNull[i] = true
		}
	}
	if scalarNull == nil && source.ScalarNull == nil {
		return source, nil
	}
	copy := *source
	copy.ScalarNull = scalarNull
	return &copy, nil
}
