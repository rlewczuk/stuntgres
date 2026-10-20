package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func cteMapExpression(host Host, view catalog.Read, expr expr.Expr, leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, error) {
	next, _, err := host.MapBoundQueryExpression(view, expr, nil, leaf)
	return next, err
}

// Restrictions are moved only when their input frame can be expressed at the
// destination. LIMIT, window inputs, rowmarks and volatile/correlated predicates
// are deliberately not commuted. The graph/schema and original expressions
// remain immutable; each folded occurrence gets its own rewritten path.
func foldCTERestrictions(host Host, view catalog.Read, root plan.Node) (plan.Node, error) {
	var rewrite func(expr.Expr) (expr.Expr, bool, error)
	rewrite = func(bound expr.Expr) (expr.Expr, bool, error) {
		if edge, ok := bound.(plan.SubqueryExpr); ok && edge.CTEEdge {
			return bound, false, nil
		}
		hasQuery := false
		plan.WalkExpression(bound, func(n expr.Expr) {
			if _, ok := n.(plan.SubqueryExpr); ok {
				hasQuery = true
			}
		})
		if !hasQuery {
			return bound, false, nil
		}
		return host.MapBoundQueryExpression(view, bound, func(child plan.Node) (plan.Node, error) { return foldCTERestrictions(host, view, child) }, nil)
	}
	return plan.RewriteQueryExpressions(root, rewrite, func(node plan.Node) (plan.Node, error) {
		if join, ok := node.(*plan.JoinNode); ok && join.Kind == parser.JoinInner && join.Predicate != nil {
			child, predicate, moves, err := moveCTEPredicates(host, view, join, join.Predicate)
			if err != nil {
				return nil, err
			}
			if moves != 0 {
				copy := *child.(*plan.JoinNode)
				copy.Predicate = predicate
				copy.Left, err = foldCTERestrictions(host, view, copy.Left)
				if err != nil {
					return nil, err
				}
				copy.Right, err = foldCTERestrictions(host, view, copy.Right)
				return &copy, err
			}
		}
		filter, ok := node.(*plan.FilterNode)
		if !ok {
			return node, nil
		}
		child, err := foldCTERestrictions(host, view, filter.Child)
		if err != nil {
			return nil, err
		}
		child, predicate, moves, err := moveCTEPredicates(host, view, child, filter.Predicate)
		if err != nil {
			return nil, err
		}
		if predicate == nil {
			return child, nil
		}
		if child == filter.Child && moves == 0 {
			return filter, nil
		}
		copy := *filter
		copy.Child, copy.Predicate = child, predicate
		return &copy, nil
	})
}

func moveCTEPredicates(host Host, view catalog.Read, child plan.Node, predicate expr.Expr) (plan.Node, expr.Expr, int, error) {
	moves := 0
	var move func(expr.Expr) (expr.Expr, error)
	move = func(predicate expr.Expr) (expr.Expr, error) {
		if and, ok := predicate.(plan.LogicalExpr); ok && and.Op == parser.LogicalAnd {
			before := moves
			left, err := move(and.Left)
			if err != nil {
				return nil, err
			}
			right, err := move(and.Right)
			if err != nil {
				return nil, err
			}
			if left == nil {
				return right, nil
			}
			if right == nil {
				return left, nil
			}
			if before == moves {
				return predicate, nil
			}
			and.Left, and.Right = left, right
			return and, nil
		}
		if predicate.Properties().Volatility == expr.Volatile {
			return predicate, nil
		}
		next, moved, err := PushCTEOccurrence(host, view, child, predicate, 0)
		if err != nil {
			return nil, err
		}
		child = next
		if moved {
			moves++
			return nil, nil
		}
		return predicate, nil
	}
	remaining, err := move(predicate)
	return child, remaining, moves, err
}

func PushCTEOccurrence(host Host, view catalog.Read, node plan.Node, predicate expr.Expr, offset int) (plan.Node, bool, error) {
	switch n := node.(type) {
	case *plan.DerivedNode:
		if n.CTE == nil || !n.CTE.Folded() {
			return node, false, nil
		}
		project := n.Query.Root.(*plan.ProjectNode)
		found, safe := false, true
		plan.WalkExpression(predicate, func(expr expr.Expr) {
			switch e := expr.(type) {
			case plan.ColumnExpr:
				if e.Frame != n.ScanFrame || e.Slot < offset || e.Slot >= offset+len(n.Schema()) {
					safe = false
				} else {
					found = true
				}
			case plan.MergeActionExpr, plan.ReturningWholeRowExpr, plan.SubqueryExpr, plan.GroupExpr, plan.AggregateExpr, plan.GroupingExpr, *plan.WindowExpr, plan.CachedOrderExpr:
				safe = false
			}
		})
		if !safe || !found {
			return node, false, nil
		}
		mapped, err := cteMapExpression(host, view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if col, ok := expr.(plan.ColumnExpr); ok {
				return project.Expressions[col.Slot-offset], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		input, moved, err := pushCTEInput(host, view, project.Child, mapped, project.Frame)
		if err != nil || !moved {
			return node, false, err
		}
		p := *project
		p.Child = input
		q := *n.Query
		q.Root, err = foldCTERestrictions(host, view, &p)
		if err != nil {
			return nil, false, err
		}
		scan := *n
		scan.Query = &q
		return &scan, true, nil
	case *plan.JoinNode:
		if n.Kind != parser.JoinInner {
			return node, false, nil
		}
		left, moved, err := PushCTEOccurrence(host, view, n.Left, predicate, offset)
		if err != nil {
			return nil, false, err
		}
		if moved {
			copy := *n
			copy.Left = left
			return &copy, true, nil
		}
		right, moved, err := PushCTEOccurrence(host, view, n.Right, predicate, offset+n.LeftWidth)
		if err != nil {
			return nil, false, err
		}
		if moved {
			copy := *n
			copy.Right = right
			return &copy, true, nil
		}
	}
	return node, false, nil
}

func pushCTEInput(host Host, view catalog.Read, node plan.Node, predicate expr.Expr, frame uint32) (plan.Node, bool, error) {
	unsafe := false
	plan.WalkExpression(predicate, func(expr expr.Expr) {
		switch n := expr.(type) {
		case plan.SubqueryExpr, plan.CachedOrderExpr, plan.AggregateExpr, plan.GroupingExpr, *plan.WindowExpr:
			unsafe = true
		case plan.ColumnExpr:
			if n.Frame != frame {
				unsafe = true
			}
		case plan.ReturningWholeRowExpr:
			unsafe = unsafe || n.Frame != frame
		case plan.MergeActionExpr:
			unsafe = unsafe || n.Frame != frame
		}
	})
	if unsafe {
		return node, false, nil
	}
	switch n := node.(type) {
	case *plan.SourceNode, *plan.SingletonNode, *plan.ValuesNode, *plan.DerivedNode, *plan.JoinNode, *plan.FilterNode, *plan.FunctionFromNode:
		return &plan.FilterNode{QuerySchema: node.Schema(), Child: node, Predicate: predicate}, true, nil
	case *plan.WindowNode, *plan.SetProjectNode:
		// Filter completed stage rows: windows and set expansion must retain
		// the definition's original input and cardinality/error demand.
		return &plan.FilterNode{QuerySchema: node.Schema(), Child: node, Predicate: predicate}, true, nil
	case *plan.SortNode:
		child, moved, err := pushCTEInput(host, view, n.Child, predicate, frame)
		if !moved || err != nil {
			return node, false, err
		}
		copy := *n
		copy.Child = child
		return &copy, true, nil
	case *plan.DistinctNode:
		mapped, err := cteMapExpression(host, view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if c, ok := expr.(plan.ColumnExpr); ok && c.Frame == frame {
				return n.Expressions[c.Slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		child, moved, err := pushCTEInput(host, view, n.Child, mapped, frame)
		if !moved || err != nil {
			return node, false, err
		}
		copy := *n
		copy.Child = child
		return &copy, true, nil
	case *plan.AggregateNode:
		// A complete ordinary grouping key is movable; subtotal/empty grouping
		// sets and aggregate-dependent restrictions retain their input frontier.
		if len(n.Keys) == 0 || n.Sets != nil && (len(n.Sets) != 1 || len(n.Sets[0]) != len(n.Keys)) {
			return node, false, nil
		}
		mapped, err := cteMapExpression(host, view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if g, ok := expr.(plan.GroupExpr); ok && g.Frame == frame {
				return n.Keys[g.Slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		child, moved, err := pushCTEInput(host, view, n.Child, mapped, frame)
		if !moved || err != nil {
			return node, false, err
		}
		copy := *n
		copy.Child = child
		return &copy, true, nil
	case *plan.SetNode:
		leftPredicate, err := cteMapExpression(host, view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if c, ok := expr.(plan.ColumnExpr); ok && c.Frame == frame {
				return n.LeftExpressions[c.Slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		rightPredicate, err := cteMapExpression(host, view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if c, ok := expr.(plan.ColumnExpr); ok && c.Frame == frame {
				return n.RightExpressions[c.Slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		left, lm, err := pushCTEInput(host, view, n.Left.Child, leftPredicate, n.Left.Frame)
		if err != nil {
			return nil, false, err
		}
		right, rm, err := pushCTEInput(host, view, n.Right.Child, rightPredicate, n.Right.Frame)
		if err != nil {
			return nil, false, err
		}
		if !lm || !rm {
			return node, false, nil
		}
		copy := *n
		l, r := *n.Left, *n.Right
		l.Child, r.Child = left, right
		copy.Left, copy.Right = &l, &r
		return &copy, true, nil
	}
	return node, false, nil
}
