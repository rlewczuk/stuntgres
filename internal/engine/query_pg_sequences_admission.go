package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// This is only the sequence view's definition qualifier frontier. It is not a
// general predicate optimizer: unknown, volatile and helper-dependent expressions
// remain in their original evaluation stage.
func mapPGSequencesPredicate(bound expr.Expr, column func(columnExpression) (expr.Expr, bool)) (expr.Expr, bool) {
	if bound == nil {
		return nil, false
	}
	switch n := bound.(type) {
	case literalExpression, parameterExpression:
		return bound, true
	case columnExpression:
		return column(n)
	case comparisonExpression:
		left, ok := mapPGSequencesPredicate(n.left, column)
		if !ok {
			return nil, false
		}
		right, ok := mapPGSequencesPredicate(n.right, column)
		if !ok {
			return nil, false
		}
		n.left, n.right = left, right
		return n, true
	case logicalExpression:
		left, ok := mapPGSequencesPredicate(n.left, column)
		if !ok {
			return nil, false
		}
		var right expr.Expr
		if n.right != nil {
			right, ok = mapPGSequencesPredicate(n.right, column)
			if !ok {
				return nil, false
			}
		}
		n.left, n.right = left, right
		return n, true
	case nullTestExpression:
		child, ok := mapPGSequencesPredicate(n.child, column)
		if !ok {
			return nil, false
		}
		n.child = child
		return n, true
	}
	return nil, false
}

// Admission must prove the whole expression belongs to the current evaluation
// frame before splitting AND or interpreting any numeric slot. In particular,
// a correlated column cannot be substituted with a coincident local slot.
func pgSequencesPredicateInFrame(predicate expr.Expr, frame uint32) bool {
	_, ok := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
		return column, frame != 0 && column.scope == 1 && column.frame == frame && column.virtual == nil
	})
	return ok
}

func admitPGSequencesPredicate(node LogicalNode, inputs *querySource, predicate expr.Expr, offset int, frame uint32) {
	if inputs == nil || !pgSequencesPredicateInFrame(predicate, frame) {
		return
	}
	switch n := node.(type) {
	case *filterNode:
		admitPGSequencesPredicate(n.child, inputs, predicate, offset, frame)
	case *rowLockNode:
		admitPGSequencesPredicate(n.child, inputs, predicate, offset, frame)
	case *joinNode:
		if n.kind != parser.JoinInner {
			return
		}
		admitPGSequencesPredicate(n.left, inputs, predicate, offset, frame)
		admitPGSequencesPredicate(n.right, inputs, predicate, offset+n.leftWidth, frame)
	case *derivedNode:
		if n.cte != nil || n.query == nil || n.scanFrame == 0 || n.scanFrame != frame {
			return
		}
		project, ok := n.query.Root.(*projectNode)
		if !ok {
			return
		}
		mapped, valid := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
			slot := column.slot - offset
			if slot < 0 || slot >= len(project.expressions) {
				return nil, false
			}
			inner, ok := project.expressions[slot].(columnExpression)
			return inner, ok && project.frame != 0 && inner.scope == 1 && inner.frame == project.frame && inner.virtual == nil && inner.slot >= 0 && inner.slot < len(project.child.Schema())
		})
		if valid {
			// Projection columns already use the child frame's join offsets.
			admitPGSequencesPredicate(project.child, inputs, mapped, 0, project.frame)
		}
	case *sourceNode:
		source := querySourceAt(inputs, n.occurrence)
		mapped, ok := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
			slot := column.slot - offset
			if slot < 0 || slot >= len(n.Schema()) {
				return nil, false
			}
			column.slot = slot
			return column, true
		})
		if !ok {
			return
		}
		admitPGSequencesSource(source, mapped, frame)
	}
}

func admitPGSequencesSource(source *querySource, predicate expr.Expr, frame uint32) {
	if source == nil || !pgSequencesPredicateInFrame(predicate, frame) {
		return
	}
	if source.pgSequences != nil {
		// Split only after every crossed frame/projection has been proven.
		// A rejected mapping must never leave behind a partially admitted AND.
		if conjunction, ok := predicate.(logicalExpression); ok && conjunction.op == parser.LogicalAnd {
			admitPGSequencesSource(source, conjunction.left, frame)
			admitPGSequencesSource(source, conjunction.right, frame)
			return
		}
		mapped, ok := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
			return column, column.slot >= 0 && column.slot < 10
		})
		if ok {
			source.pgSequences.predicates = append(source.pgSequences.predicates, mapped)
		}
		return
	}
	if source.input != nil {
		mapped, ok := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
			if column.slot < 0 || column.slot >= len(source.layout) || source.layout[column.slot] < 0 {
				return nil, false
			}
			column.slot = source.layout[column.slot]
			return column, true
		})
		if ok {
			// This layout adapts the row, not its lexical evaluation frame.
			admitPGSequencesSource(source.input, mapped, frame)
		}
		return
	}
	if project, ok := source.nestedRoot.(*projectNode); ok {
		mapped, valid := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
			if column.slot < 0 || column.slot >= len(project.expressions) {
				return nil, false
			}
			inner, ok := project.expressions[column.slot].(columnExpression)
			return inner, ok && project.frame != 0 && inner.scope == 1 && inner.frame == project.frame && inner.virtual == nil && inner.slot >= 0 && inner.slot < len(project.child.Schema())
		})
		if valid {
			admitPGSequencesPredicate(project.child, source.nestedInputs, mapped, 0, project.frame)
		}
	}
}
