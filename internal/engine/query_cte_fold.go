package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func cteMapExpression(view catalog.Read, expr expr.Expr, leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, error) {
	next, _, err := mapBoundExpression(view, expr, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, leaf)
	return next, err
}

// Restrictions are moved only when their input frame can be expressed at the
// destination. LIMIT, window inputs, rowmarks and volatile/correlated predicates
// are deliberately not commuted. The graph/schema and original expressions
// remain immutable; each folded occurrence gets its own rewritten path.
func foldCTERestrictions(view catalog.Read, root LogicalNode) (LogicalNode, error) {
	var rewrite func(expr.Expr) (expr.Expr, bool, error)
	rewrite = func(bound expr.Expr) (expr.Expr, bool, error) {
		if edge, ok := bound.(subqueryExpression); ok && edge.CTEDefinition {
			return bound, false, nil
		}
		hasQuery := false
		walkBoundExpression(bound, func(n expr.Expr) {
			if _, ok := n.(subqueryExpression); ok {
				hasQuery = true
			}
		})
		if !hasQuery {
			return bound, false, nil
		}
		return mapBoundExpression(view, bound, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(child LogicalNode) (LogicalNode, error) { return foldCTERestrictions(view, child) }, nil, nil)
	}
	return rewriteQueryExpressions(root, rewrite, func(node LogicalNode) (LogicalNode, error) {
		if join, ok := node.(*joinNode); ok && join.Kind == parser.JoinInner && join.Predicate != nil {
			child, predicate, moves, err := moveCTEPredicates(view, join, join.Predicate)
			if err != nil {
				return nil, err
			}
			if moves != 0 {
				copy := *child.(*joinNode)
				copy.Predicate = predicate
				copy.Left, err = foldCTERestrictions(view, copy.Left)
				if err != nil {
					return nil, err
				}
				copy.Right, err = foldCTERestrictions(view, copy.Right)
				return &copy, err
			}
		}
		filter, ok := node.(*filterNode)
		if !ok {
			return node, nil
		}
		child, err := foldCTERestrictions(view, filter.Child)
		if err != nil {
			return nil, err
		}
		child, predicate, moves, err := moveCTEPredicates(view, child, filter.Predicate)
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

func moveCTEPredicates(view catalog.Read, child LogicalNode, predicate expr.Expr) (LogicalNode, expr.Expr, int, error) {
	moves := 0
	var move func(expr.Expr) (expr.Expr, error)
	move = func(predicate expr.Expr) (expr.Expr, error) {
		if and, ok := predicate.(logicalExpression); ok && and.Op == parser.LogicalAnd {
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
		next, moved, err := pushCTEOccurrence(view, child, predicate, 0)
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

func pushCTEOccurrence(view catalog.Read, node LogicalNode, predicate expr.Expr, offset int) (LogicalNode, bool, error) {
	switch n := node.(type) {
	case *derivedNode:
		if n.CTE == nil || !n.CTE.Folded() {
			return node, false, nil
		}
		project := n.Query.Root.(*projectNode)
		found, safe := false, true
		walkBoundExpression(predicate, func(expr expr.Expr) {
			switch e := expr.(type) {
			case columnExpression:
				if e.Frame != n.ScanFrame || e.Slot < offset || e.Slot >= offset+len(n.Schema()) {
					safe = false
				} else {
					found = true
				}
			case mergeActionExpression, returningWholeRowExpression, subqueryExpression, groupExpression, aggregateExpression, groupingExpression, *windowExpression, cachedOrderExpression:
				safe = false
			}
		})
		if !safe || !found {
			return node, false, nil
		}
		mapped, err := cteMapExpression(view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if col, ok := expr.(columnExpression); ok {
				return project.Expressions[col.Slot-offset], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		input, moved, err := pushCTEInput(view, project.Child, mapped, project.Frame)
		if err != nil || !moved {
			return node, false, err
		}
		p := *project
		p.Child = input
		q := *n.Query
		q.Root, err = foldCTERestrictions(view, &p)
		if err != nil {
			return nil, false, err
		}
		scan := *n
		scan.Query = &q
		return &scan, true, nil
	case *joinNode:
		if n.Kind != parser.JoinInner {
			return node, false, nil
		}
		left, moved, err := pushCTEOccurrence(view, n.Left, predicate, offset)
		if err != nil {
			return nil, false, err
		}
		if moved {
			copy := *n
			copy.Left = left
			return &copy, true, nil
		}
		right, moved, err := pushCTEOccurrence(view, n.Right, predicate, offset+n.LeftWidth)
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

func pushCTEInput(view catalog.Read, node LogicalNode, predicate expr.Expr, frame uint32) (LogicalNode, bool, error) {
	unsafe := false
	walkBoundExpression(predicate, func(expr expr.Expr) {
		switch n := expr.(type) {
		case subqueryExpression, cachedOrderExpression, aggregateExpression, groupingExpression, *windowExpression:
			unsafe = true
		case columnExpression:
			if n.Frame != frame {
				unsafe = true
			}
		case returningWholeRowExpression:
			unsafe = unsafe || n.Frame != frame
		case mergeActionExpression:
			unsafe = unsafe || n.Frame != frame
		}
	})
	if unsafe {
		return node, false, nil
	}
	switch n := node.(type) {
	case *sourceNode, *singletonNode, *valuesNode, *derivedNode, *joinNode, *filterNode, *functionFromNode:
		return &filterNode{QuerySchema: node.Schema(), Child: node, Predicate: predicate}, true, nil
	case *windowNode, *setProjectNode:
		// Filter completed stage rows: windows and set expansion must retain
		// the definition's original input and cardinality/error demand.
		return &filterNode{QuerySchema: node.Schema(), Child: node, Predicate: predicate}, true, nil
	case *sortNode:
		child, moved, err := pushCTEInput(view, n.Child, predicate, frame)
		if !moved || err != nil {
			return node, false, err
		}
		copy := *n
		copy.Child = child
		return &copy, true, nil
	case *distinctNode:
		mapped, err := cteMapExpression(view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if c, ok := expr.(columnExpression); ok && c.Frame == frame {
				return n.Expressions[c.Slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		child, moved, err := pushCTEInput(view, n.Child, mapped, frame)
		if !moved || err != nil {
			return node, false, err
		}
		copy := *n
		copy.Child = child
		return &copy, true, nil
	case *aggregateNode:
		// A complete ordinary grouping key is movable; subtotal/empty grouping
		// sets and aggregate-dependent restrictions retain their input frontier.
		if len(n.Keys) == 0 || n.Sets != nil && (len(n.Sets) != 1 || len(n.Sets[0]) != len(n.Keys)) {
			return node, false, nil
		}
		mapped, err := cteMapExpression(view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if g, ok := expr.(groupExpression); ok && g.Frame == frame {
				return n.Keys[g.Slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		child, moved, err := pushCTEInput(view, n.Child, mapped, frame)
		if !moved || err != nil {
			return node, false, err
		}
		copy := *n
		copy.Child = child
		return &copy, true, nil
	case *setNode:
		leftPredicate, err := cteMapExpression(view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if c, ok := expr.(columnExpression); ok && c.Frame == frame {
				return n.LeftExpressions[c.Slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		rightPredicate, err := cteMapExpression(view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if c, ok := expr.(columnExpression); ok && c.Frame == frame {
				return n.RightExpressions[c.Slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		left, lm, err := pushCTEInput(view, n.Left.Child, leftPredicate, n.Left.Frame)
		if err != nil {
			return nil, false, err
		}
		right, rm, err := pushCTEInput(view, n.Right.Child, rightPredicate, n.Right.Frame)
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
