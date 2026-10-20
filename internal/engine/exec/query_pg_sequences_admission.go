package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// This is only the sequence view's definition qualifier frontier. It is not a
// general predicate optimizer: unknown, volatile and helper-dependent expressions
// remain in their original evaluation stage.
func mapPGSequencesPredicate(bound expr.Expr, column func(plan.ColumnExpr) (expr.Expr, bool)) (expr.Expr, bool) {
	if bound == nil {
		return nil, false
	}
	switch n := bound.(type) {
	case plan.LiteralExpr, plan.ParameterExpr:
		return bound, true
	case plan.ColumnExpr:
		return column(n)
	case plan.ComparisonExpr:
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
	case plan.LogicalExpr:
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
	case plan.NullTestExpr:
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
func PGSequencesPredicateInFrame(predicate expr.Expr, frame uint32) bool {
	_, ok := mapPGSequencesPredicate(predicate, func(column plan.ColumnExpr) (expr.Expr, bool) {
		return column, frame != 0 && column.Scope == 1 && column.Frame == frame && column.Virtual == nil
	})
	return ok
}

func AdmitPGSequencesPredicate(node plan.Node, inputs *Source, predicate expr.Expr, offset int, frame uint32) {
	if inputs == nil || !PGSequencesPredicateInFrame(predicate, frame) {
		return
	}
	switch n := node.(type) {
	case *plan.FilterNode:
		AdmitPGSequencesPredicate(n.Child, inputs, predicate, offset, frame)
	case *plan.RowLockNode:
		AdmitPGSequencesPredicate(n.Child, inputs, predicate, offset, frame)
	case *plan.JoinNode:
		if n.Kind != parser.JoinInner {
			return
		}
		AdmitPGSequencesPredicate(n.Left, inputs, predicate, offset, frame)
		AdmitPGSequencesPredicate(n.Right, inputs, predicate, offset+n.LeftWidth, frame)
	case *plan.DerivedNode:
		if n.CTE != nil || n.Query == nil || n.ScanFrame == 0 || n.ScanFrame != frame {
			return
		}
		project, ok := n.Query.Root.(*plan.ProjectNode)
		if !ok {
			return
		}
		mapped, valid := mapPGSequencesPredicate(predicate, func(column plan.ColumnExpr) (expr.Expr, bool) {
			slot := column.Slot - offset
			if slot < 0 || slot >= len(project.Expressions) {
				return nil, false
			}
			inner, ok := project.Expressions[slot].(plan.ColumnExpr)
			return inner, ok && project.Frame != 0 && inner.Scope == 1 && inner.Frame == project.Frame && inner.Virtual == nil && inner.Slot >= 0 && inner.Slot < len(project.Child.Schema())
		})
		if valid {
			// Projection columns already use the child frame's join offsets.
			AdmitPGSequencesPredicate(project.Child, inputs, mapped, 0, project.Frame)
		}
	case *plan.SourceNode:
		source := QuerySourceAt(inputs, n.Occurrence)
		mapped, ok := mapPGSequencesPredicate(predicate, func(column plan.ColumnExpr) (expr.Expr, bool) {
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
		AdmitPGSequencesSource(source, mapped, frame)
	}
}

func AdmitPGSequencesSource(source *Source, predicate expr.Expr, frame uint32) {
	if source == nil || !PGSequencesPredicateInFrame(predicate, frame) {
		return
	}
	if source.PGSequences != nil {
		// Split only after every crossed frame/projection has been proven.
		// A rejected mapping must never leave behind a partially admitted AND.
		if conjunction, ok := predicate.(plan.LogicalExpr); ok && conjunction.Op == parser.LogicalAnd {
			AdmitPGSequencesSource(source, conjunction.Left, frame)
			AdmitPGSequencesSource(source, conjunction.Right, frame)
			return
		}
		mapped, ok := mapPGSequencesPredicate(predicate, func(column plan.ColumnExpr) (expr.Expr, bool) {
			return column, column.Slot >= 0 && column.Slot < 10
		})
		if ok {
			source.PGSequences.Predicates = append(source.PGSequences.Predicates, mapped)
		}
		return
	}
	if source.Input != nil {
		mapped, ok := mapPGSequencesPredicate(predicate, func(column plan.ColumnExpr) (expr.Expr, bool) {
			if column.Slot < 0 || column.Slot >= len(source.Layout) || source.Layout[column.Slot] < 0 {
				return nil, false
			}
			column.Slot = source.Layout[column.Slot]
			return column, true
		})
		if ok {
			// This layout adapts the row, not its lexical evaluation frame.
			AdmitPGSequencesSource(source.Input, mapped, frame)
		}
		return
	}
	if project, ok := source.NestedRoot.(*plan.ProjectNode); ok {
		mapped, valid := mapPGSequencesPredicate(predicate, func(column plan.ColumnExpr) (expr.Expr, bool) {
			if column.Slot < 0 || column.Slot >= len(project.Expressions) {
				return nil, false
			}
			inner, ok := project.Expressions[column.Slot].(plan.ColumnExpr)
			return inner, ok && project.Frame != 0 && inner.Scope == 1 && inner.Frame == project.Frame && inner.Virtual == nil && inner.Slot >= 0 && inner.Slot < len(project.Child.Schema())
		})
		if valid {
			AdmitPGSequencesPredicate(project.Child, source.NestedInputs, mapped, 0, project.Frame)
		}
	}
}
