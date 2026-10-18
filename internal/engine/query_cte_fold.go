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
		if edge, ok := bound.(subqueryExpression); ok && edge.cteDefinition {
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
		if join, ok := node.(*joinNode); ok && join.kind == parser.JoinInner && join.predicate != nil {
			child, predicate, moves, err := moveCTEPredicates(view, join, join.predicate)
			if err != nil {
				return nil, err
			}
			if moves != 0 {
				copy := *child.(*joinNode)
				copy.predicate = predicate
				copy.left, err = foldCTERestrictions(view, copy.left)
				if err != nil {
					return nil, err
				}
				copy.right, err = foldCTERestrictions(view, copy.right)
				return &copy, err
			}
		}
		filter, ok := node.(*filterNode)
		if !ok {
			return node, nil
		}
		child, err := foldCTERestrictions(view, filter.child)
		if err != nil {
			return nil, err
		}
		child, predicate, moves, err := moveCTEPredicates(view, child, filter.predicate)
		if err != nil {
			return nil, err
		}
		if predicate == nil {
			return child, nil
		}
		if child == filter.child && moves == 0 {
			return filter, nil
		}
		copy := *filter
		copy.child, copy.predicate = child, predicate
		return &copy, nil
	})
}

func moveCTEPredicates(view catalog.Read, child LogicalNode, predicate expr.Expr) (LogicalNode, expr.Expr, int, error) {
	moves := 0
	var move func(expr.Expr) (expr.Expr, error)
	move = func(predicate expr.Expr) (expr.Expr, error) {
		if and, ok := predicate.(logicalExpression); ok && and.op == parser.LogicalAnd {
			before := moves
			left, err := move(and.left)
			if err != nil {
				return nil, err
			}
			right, err := move(and.right)
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
			and.left, and.right = left, right
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
		if n.cte == nil || !n.cte.folded() {
			return node, false, nil
		}
		project := n.query.Root.(*projectNode)
		found, safe := false, true
		walkBoundExpression(predicate, func(expr expr.Expr) {
			switch e := expr.(type) {
			case columnExpression:
				if e.frame != n.scanFrame || e.slot < offset || e.slot >= offset+len(n.Schema()) {
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
				return project.expressions[col.slot-offset], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		input, moved, err := pushCTEInput(view, project.child, mapped, project.frame)
		if err != nil || !moved {
			return node, false, err
		}
		p := *project
		p.child = input
		q := *n.query
		q.Root, err = foldCTERestrictions(view, &p)
		if err != nil {
			return nil, false, err
		}
		scan := *n
		scan.query = &q
		return &scan, true, nil
	case *joinNode:
		if n.kind != parser.JoinInner {
			return node, false, nil
		}
		left, moved, err := pushCTEOccurrence(view, n.left, predicate, offset)
		if err != nil {
			return nil, false, err
		}
		if moved {
			copy := *n
			copy.left = left
			return &copy, true, nil
		}
		right, moved, err := pushCTEOccurrence(view, n.right, predicate, offset+n.leftWidth)
		if err != nil {
			return nil, false, err
		}
		if moved {
			copy := *n
			copy.right = right
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
			if n.frame != frame {
				unsafe = true
			}
		case returningWholeRowExpression:
			unsafe = unsafe || n.frame != frame
		case mergeActionExpression:
			unsafe = unsafe || n.frame != frame
		}
	})
	if unsafe {
		return node, false, nil
	}
	switch n := node.(type) {
	case *sourceNode, *singletonNode, *valuesNode, *derivedNode, *joinNode, *filterNode, *functionFromNode:
		return &filterNode{querySchema: node.Schema(), child: node, predicate: predicate}, true, nil
	case *windowNode, *setProjectNode:
		// Filter completed stage rows: windows and set expansion must retain
		// the definition's original input and cardinality/error demand.
		return &filterNode{querySchema: node.Schema(), child: node, predicate: predicate}, true, nil
	case *sortNode:
		child, moved, err := pushCTEInput(view, n.child, predicate, frame)
		if !moved || err != nil {
			return node, false, err
		}
		copy := *n
		copy.child = child
		return &copy, true, nil
	case *distinctNode:
		mapped, err := cteMapExpression(view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if c, ok := expr.(columnExpression); ok && c.frame == frame {
				return n.expressions[c.slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		child, moved, err := pushCTEInput(view, n.child, mapped, frame)
		if !moved || err != nil {
			return node, false, err
		}
		copy := *n
		copy.child = child
		return &copy, true, nil
	case *aggregateNode:
		// A complete ordinary grouping key is movable; subtotal/empty grouping
		// sets and aggregate-dependent restrictions retain their input frontier.
		if len(n.keys) == 0 || n.sets != nil && (len(n.sets) != 1 || len(n.sets[0]) != len(n.keys)) {
			return node, false, nil
		}
		mapped, err := cteMapExpression(view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if g, ok := expr.(groupExpression); ok && g.frame == frame {
				return n.keys[g.slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		child, moved, err := pushCTEInput(view, n.child, mapped, frame)
		if !moved || err != nil {
			return node, false, err
		}
		copy := *n
		copy.child = child
		return &copy, true, nil
	case *setNode:
		leftPredicate, err := cteMapExpression(view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if c, ok := expr.(columnExpression); ok && c.frame == frame {
				return n.leftExpressions[c.slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		rightPredicate, err := cteMapExpression(view, predicate, func(expr expr.Expr) (expr.Expr, bool, error) {
			if c, ok := expr.(columnExpression); ok && c.frame == frame {
				return n.rightExpressions[c.slot], true, nil
			}
			return expr, false, nil
		})
		if err != nil {
			return nil, false, err
		}
		left, lm, err := pushCTEInput(view, n.left.child, leftPredicate, n.left.frame)
		if err != nil {
			return nil, false, err
		}
		right, rm, err := pushCTEInput(view, n.right.child, rightPredicate, n.right.frame)
		if err != nil {
			return nil, false, err
		}
		if !lm || !rm {
			return node, false, nil
		}
		copy := *n
		l, r := *n.left, *n.right
		l.child, r.child = left, right
		copy.left, copy.right = &l, &r
		return &copy, true, nil
	}
	return node, false, nil
}
