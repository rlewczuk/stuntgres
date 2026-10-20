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
		left, ok := mapPGSequencesPredicate(n.Left, column)
		if !ok {
			return nil, false
		}
		right, ok := mapPGSequencesPredicate(n.Right, column)
		if !ok {
			return nil, false
		}
		n.Left, n.Right = left, right
		return n, true
	case logicalExpression:
		left, ok := mapPGSequencesPredicate(n.Left, column)
		if !ok {
			return nil, false
		}
		var right expr.Expr
		if n.Right != nil {
			right, ok = mapPGSequencesPredicate(n.Right, column)
			if !ok {
				return nil, false
			}
		}
		n.Left, n.Right = left, right
		return n, true
	case nullTestExpression:
		child, ok := mapPGSequencesPredicate(n.Child, column)
		if !ok {
			return nil, false
		}
		n.Child = child
		return n, true
	}
	return nil, false
}

// Admission must prove the whole expression belongs to the current evaluation
// frame before splitting AND or interpreting any numeric slot. In particular,
// a correlated column cannot be substituted with a coincident local slot.
func pgSequencesPredicateInFrame(predicate expr.Expr, frame uint32) bool {
	_, ok := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
		return column, frame != 0 && column.Scope == 1 && column.Frame == frame && column.Virtual == nil
	})
	return ok
}

func admitPGSequencesPredicate(node LogicalNode, inputs *querySource, predicate expr.Expr, offset int, frame uint32) {
	if inputs == nil || !pgSequencesPredicateInFrame(predicate, frame) {
		return
	}
	switch n := node.(type) {
	case *filterNode:
		admitPGSequencesPredicate(n.Child, inputs, predicate, offset, frame)
	case *rowLockNode:
		admitPGSequencesPredicate(n.Child, inputs, predicate, offset, frame)
	case *joinNode:
		if n.Kind != parser.JoinInner {
			return
		}
		admitPGSequencesPredicate(n.Left, inputs, predicate, offset, frame)
		admitPGSequencesPredicate(n.Right, inputs, predicate, offset+n.LeftWidth, frame)
	case *derivedNode:
		if n.CTE != nil || n.Query == nil || n.ScanFrame == 0 || n.ScanFrame != frame {
			return
		}
		project, ok := n.Query.Root.(*projectNode)
		if !ok {
			return
		}
		mapped, valid := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
			slot := column.Slot - offset
			if slot < 0 || slot >= len(project.Expressions) {
				return nil, false
			}
			inner, ok := project.Expressions[slot].(columnExpression)
			return inner, ok && project.Frame != 0 && inner.Scope == 1 && inner.Frame == project.Frame && inner.Virtual == nil && inner.Slot >= 0 && inner.Slot < len(project.Child.Schema())
		})
		if valid {
			// Projection columns already use the child frame's join offsets.
			admitPGSequencesPredicate(project.Child, inputs, mapped, 0, project.Frame)
		}
	case *sourceNode:
		source := querySourceAt(inputs, n.Occurrence)
		mapped, ok := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
			slot := column.Slot - offset
			if slot < 0 || slot >= len(n.Schema()) {
				return nil, false
			}
			column.Slot = slot
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
		if conjunction, ok := predicate.(logicalExpression); ok && conjunction.Op == parser.LogicalAnd {
			admitPGSequencesSource(source, conjunction.Left, frame)
			admitPGSequencesSource(source, conjunction.Right, frame)
			return
		}
		mapped, ok := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
			return column, column.Slot >= 0 && column.Slot < 10
		})
		if ok {
			source.pgSequences.predicates = append(source.pgSequences.predicates, mapped)
		}
		return
	}
	if source.input != nil {
		mapped, ok := mapPGSequencesPredicate(predicate, func(column columnExpression) (expr.Expr, bool) {
			if column.Slot < 0 || column.Slot >= len(source.layout) || source.layout[column.Slot] < 0 {
				return nil, false
			}
			column.Slot = source.layout[column.Slot]
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
			if column.Slot < 0 || column.Slot >= len(project.Expressions) {
				return nil, false
			}
			inner, ok := project.Expressions[column.Slot].(columnExpression)
			return inner, ok && project.Frame != 0 && inner.Scope == 1 && inner.Frame == project.Frame && inner.Virtual == nil && inner.Slot >= 0 && inner.Slot < len(project.Child.Schema())
		})
		if valid {
			admitPGSequencesPredicate(project.Child, source.nestedInputs, mapped, 0, project.Frame)
		}
	}
}
