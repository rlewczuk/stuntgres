package parser

import "strings"

// Scalar admission does not grant DEFAULT admission. Check the complete default
// tree before publishing a utility AST, retaining the existing Prepare boundary.
func (l *lexer) defaultIndexPropertyAllowed(raw Expr) bool {
	if position, found := firstDefaultIndexProperty(raw); found {
		l.failAt("0A000", "functions are unsupported", position.Offset)
		return false
	}
	return true
}

func firstDefaultIndexProperty(raw Expr) (Position, bool) {
	var first Position
	found := false
	child := func(raw Expr) {
		position, exists := firstDefaultIndexProperty(raw)
		if exists && (!found || position.Offset < first.Offset) {
			first, found = position, true
		}
	}
	var args []Expr
	switch e := raw.(type) {
	case FunctionExpr:
		if len(e.Name.Parts) != 0 {
			name := e.Name.Parts[len(e.Name.Parts)-1].Text
			if strings.EqualFold(name, "pg_indexam_has_property") || strings.EqualFold(name, "pg_index_has_property") || strings.EqualFold(name, "pg_index_column_has_property") {
				return e.Pos, true
			}
		}
		args = e.Args
		if e.Window != nil {
			for _, key := range e.Window.Partition {
				child(key)
			}
			for _, key := range e.Window.Order {
				child(key.Expr)
			}
			for _, offset := range e.Window.Offsets() {
				child(offset)
			}
		}
	case CallExpr:
		args = e.Args
	case FieldExpr:
		child(e.Base)
	case RowExpr:
		args = e.Fields
	case InListExpr:
		child(e.Left)
		args = e.Items
	case ArrayComparisonExpr:
		child(e.Left)
		child(e.Right)
	case ArrayExpr:
		args = e.Elements
	case SubscriptExpr:
		child(e.Base)
		for _, s := range e.Subscripts {
			child(s.Lower)
			child(s.Upper)
		}
	case OperatorExpr:
		args = e.Args
	case ConditionalExpr:
		args = e.Args
	case GroupingExpr:
		args = e.Args
	case AggregateExpr:
		args = e.Args
		for _, key := range e.Order {
			child(key.Expr)
		}
		child(e.Filter)
		if e.Window != nil {
			for _, key := range e.Window.Partition {
				child(key)
			}
			for _, key := range e.Window.Order {
				child(key.Expr)
			}
			for _, offset := range e.Window.Offsets() {
				child(offset)
			}
		}
	case CaseExpr:
		child(e.Test)
		for _, arm := range e.Arms {
			child(arm.Condition)
			child(arm.Result)
		}
		child(e.Else)
	case CastExpr:
		child(e.Child)
	case RegclassExpr:
		child(e.Child)
	case CollateExpr:
		child(e.Child)
	case TimeZoneExpr:
		child(e.Value)
		if !e.Local {
			child(e.Zone)
		}
	case LogicalExpr:
		child(e.Left)
		child(e.Right)
	case PredicateExpr:
		child(e.Left)
		child(e.Right)
	}
	for _, arg := range args {
		child(arg)
	}
	return first, found
}
