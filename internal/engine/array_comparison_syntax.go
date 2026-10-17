package engine

import "stuntgres/internal/parser"

// Stored syntax follows analyzed lowering, including independently typed IN
// operands and erased quantified constructor casts. Only changed ancestors are
// copied; child queries normalize in their own lexical binding frame.
func (b *expressionBinder) normalizeStoredExpression(raw parser.Expr) parser.Expr {
	if len(b.storedExpressionSyntax) == 0 {
		return raw
	}
	next, _ := b.normalizeInListExpression(raw)
	return next
}

func (b *expressionBinder) normalizeInListExpression(raw parser.Expr) (parser.Expr, bool) {
	changed := false
	child := func(raw parser.Expr) parser.Expr {
		next, did := b.normalizeInListExpression(raw)
		changed = changed || did
		return next
	}
	args := func(raw []parser.Expr) []parser.Expr {
		next, did := b.normalizeInListArguments(raw)
		changed = changed || did
		return next
	}
	order := func(raw []parser.OrderKey) []parser.OrderKey {
		next, did := b.normalizeInListOrder(raw)
		changed = changed || did
		return next
	}
	window := func(raw *parser.WindowSpec) *parser.WindowSpec {
		next, did := b.normalizeInListWindow(raw)
		changed = changed || did
		return next
	}
	if raw != nil {
		switch raw.(type) {
		case parser.InListExpr, parser.CastExpr:
			if lowered, ok := b.storedExpressionSyntax[storedExpressionSyntaxKey{frame: b.frameID(), position: raw.SourcePosition()}]; ok {
				return b.normalizeStoredExpression(lowered), true
			}
		}
	}
	switch r := raw.(type) {
	case parser.InListExpr:
		r.Left, r.Items = child(r.Left), args(r.Items)
		if changed {
			return r, true
		}
	case parser.ArrayComparisonExpr:
		r.Left, r.Right = child(r.Left), child(r.Right)
		if changed {
			return r, true
		}
	case parser.OperatorExpr:
		r.Args = args(r.Args)
		if changed {
			return r, true
		}
	case parser.LogicalExpr:
		r.Left, r.Right = child(r.Left), child(r.Right)
		if changed {
			return r, true
		}
	case parser.PredicateExpr:
		r.Left, r.Right = child(r.Left), child(r.Right)
		if changed {
			return r, true
		}
	case parser.SubqueryExpr:
		r.Left = child(r.Left)
		if changed {
			return r, true
		}
	case parser.ArrayExpr:
		r.Elements = args(r.Elements)
		if changed {
			return r, true
		}
	case parser.SubscriptExpr:
		r.Base = child(r.Base)
		var subs []parser.ArraySubscript
		for i, sub := range r.Subscripts {
			lower, lc := b.normalizeInListExpression(sub.Lower)
			upper, uc := b.normalizeInListExpression(sub.Upper)
			if lc || uc {
				if subs == nil {
					subs = append([]parser.ArraySubscript(nil), r.Subscripts...)
				}
				sub.Lower, sub.Upper = lower, upper
				subs[i] = sub
				changed = true
			}
		}
		if subs != nil {
			r.Subscripts = subs
		}
		if changed {
			return r, true
		}
	case parser.CastExpr:
		r.Child = child(r.Child)
		if changed {
			return r, true
		}
	case parser.CollateExpr:
		r.Child = child(r.Child)
		if changed {
			return r, true
		}
	case parser.RegclassExpr:
		r.Child = child(r.Child)
		if changed {
			return r, true
		}
	case parser.TimeZoneExpr:
		r.Value, r.Zone = child(r.Value), child(r.Zone)
		if changed {
			return r, true
		}
	case parser.FieldExpr:
		r.Base = child(r.Base)
		if changed {
			return r, true
		}
	case parser.RowExpr:
		r.Fields = args(r.Fields)
		if changed {
			return r, true
		}
	case parser.GroupingExpr:
		r.Args = args(r.Args)
		if changed {
			return r, true
		}
	case parser.ConditionalExpr:
		r.Args = args(r.Args)
		if changed {
			return r, true
		}
	case parser.CaseExpr:
		r.Test, r.Else = child(r.Test), child(r.Else)
		var arms []parser.CaseArm
		for i, arm := range r.Arms {
			condition, cc := b.normalizeInListExpression(arm.Condition)
			result, rc := b.normalizeInListExpression(arm.Result)
			if cc || rc {
				if arms == nil {
					arms = append([]parser.CaseArm(nil), r.Arms...)
				}
				arm.Condition, arm.Result = condition, result
				arms[i] = arm
				changed = true
			}
		}
		if arms != nil {
			r.Arms = arms
		}
		if changed {
			return r, true
		}
	case parser.SQLJSONExpr:
		r.Args = args(r.Args)
		if changed {
			return r, true
		}
	case parser.JSONPredicateExpr:
		r.Child = child(r.Child)
		if changed {
			return r, true
		}
	case parser.FunctionExpr:
		r.Args, r.Order, r.Filter, r.Window = args(r.Args), order(r.Order), child(r.Filter), window(r.Window)
		if changed {
			return r, true
		}
	case parser.AggregateExpr:
		r.Args, r.Order, r.Filter, r.Window = args(r.Args), order(r.Order), child(r.Filter), window(r.Window)
		if changed {
			return r, true
		}
	case parser.CallExpr:
		r.Args = args(r.Args)
		if changed {
			return r, true
		}
	}
	return raw, false
}

func (b *expressionBinder) normalizeInListArguments(raw []parser.Expr) ([]parser.Expr, bool) {
	var result []parser.Expr
	for i, item := range raw {
		next, changed := b.normalizeInListExpression(item)
		if changed {
			if result == nil {
				result = append([]parser.Expr(nil), raw...)
			}
			result[i] = next
		}
	}
	if result != nil {
		return result, true
	}
	return raw, false
}

func (b *expressionBinder) normalizeInListOrder(raw []parser.OrderKey) ([]parser.OrderKey, bool) {
	var result []parser.OrderKey
	for i, key := range raw {
		next, changed := b.normalizeInListExpression(key.Expr)
		if changed {
			if result == nil {
				result = append([]parser.OrderKey(nil), raw...)
			}
			key.Expr = next
			result[i] = key
		}
	}
	if result != nil {
		return result, true
	}
	return raw, false
}

func (b *expressionBinder) normalizeInListWindow(raw *parser.WindowSpec) (*parser.WindowSpec, bool) {
	if raw == nil {
		return raw, false
	}
	next := *raw
	partition, pc := b.normalizeInListArguments(raw.Partition)
	order, oc := b.normalizeInListOrder(raw.Order)
	next.Partition, next.Order = partition, order
	changed := pc || oc
	if raw.Frame != nil {
		start, sc := b.normalizeInListExpression(raw.Frame.Start.Offset)
		end, ec := b.normalizeInListExpression(raw.Frame.End.Offset)
		if sc || ec {
			frame := *raw.Frame
			frame.Start.Offset, frame.End.Offset = start, end
			next.Frame = &frame
			changed = true
		}
	}
	if changed {
		return &next, true
	}
	return raw, false
}

func (b *expressionBinder) normalizeStoredQuerySyntax(s *parser.Select) {
	if len(b.storedExpressionSyntax) == 0 {
		return
	}
	s.Targets, _ = b.normalizeInListArguments(s.Targets)
	s.Where, s.Having = b.normalizeStoredExpression(s.Where), b.normalizeStoredExpression(s.Having)
	s.From = b.normalizeInListFrom(s.From)
	if s.Order != nil {
		if keys, changed := b.normalizeInListOrder(s.Order.Keys); changed {
			order := *s.Order
			order.Keys = keys
			s.Order = &order
		}
	}
	if s.Distinct != nil {
		if on, changed := b.normalizeInListArguments(s.Distinct.On); changed {
			distinct := *s.Distinct
			distinct.On = on
			s.Distinct = &distinct
		}
	}
	if s.GroupBy != nil {
		if items, changed := b.normalizeInListGroups(s.GroupBy.Items); changed {
			group := *s.GroupBy
			group.Items = items
			s.GroupBy = &group
		}
	}
	var windows []parser.WindowDefinition
	for i, definition := range s.Windows {
		if spec, changed := b.normalizeInListWindow(&definition.Spec); changed {
			if windows == nil {
				windows = append([]parser.WindowDefinition(nil), s.Windows...)
			}
			definition.Spec = *spec
			windows[i] = definition
		}
	}
	if windows != nil {
		s.Windows = windows
	}
	s.Bounds = b.normalizeInListBounds(s.Bounds)
	if s.Values != nil {
		s.Values = b.normalizeInListValues(s.Values)
	}
}

func (b *expressionBinder) normalizeInListGroups(raw []parser.GroupItem) ([]parser.GroupItem, bool) {
	var result []parser.GroupItem
	for i, item := range raw {
		expr, ec := b.normalizeInListExpression(item.Expr)
		items, ic := b.normalizeInListGroups(item.Items)
		if ec || ic {
			if result == nil {
				result = append([]parser.GroupItem(nil), raw...)
			}
			item.Expr, item.Items = expr, items
			result[i] = item
		}
	}
	if result != nil {
		return result, true
	}
	return raw, false
}

func (b *expressionBinder) normalizeInListBounds(raw *parser.QueryBounds) *parser.QueryBounds {
	if raw == nil {
		return raw
	}
	offset, oc := b.normalizeInListExpression(raw.Offset)
	count, cc := b.normalizeInListExpression(raw.Count)
	if !oc && !cc {
		return raw
	}
	next := *raw
	next.Offset, next.Count = offset, count
	return &next
}

func (b *expressionBinder) normalizeInListValues(raw *parser.ValuesQuery) *parser.ValuesQuery {
	var rows [][]parser.Expr
	for i, row := range raw.Rows {
		if next, changed := b.normalizeInListArguments(row); changed {
			if rows == nil {
				rows = append([][]parser.Expr(nil), raw.Rows...)
			}
			rows[i] = next
		}
	}
	bounds := b.normalizeInListBounds(raw.Bounds)
	order := raw.Order
	if order != nil {
		if keys, changed := b.normalizeInListOrder(order.Keys); changed {
			next := *order
			next.Keys = keys
			order = &next
		}
	}
	if rows == nil && bounds == raw.Bounds && order == raw.Order {
		return raw
	}
	next := *raw
	if rows != nil {
		next.Rows = rows
	}
	next.Bounds, next.Order = bounds, order
	return &next
}

func (b *expressionBinder) normalizeInListFrom(raw parser.FromItem) parser.FromItem {
	switch r := raw.(type) {
	case *parser.JoinReference:
		left, right := b.normalizeInListFrom(r.Left), b.normalizeInListFrom(r.Right)
		on, changed := b.normalizeInListExpression(r.On)
		if changed || left != r.Left || right != r.Right {
			next := *r
			next.Left, next.Right, next.On = left, right, on
			return &next
		}
	case *parser.AliasReference:
		input := b.normalizeInListFrom(r.Input)
		if input != r.Input {
			next := *r
			next.Input = input
			return &next
		}
	case *parser.ValuesQuery:
		return b.normalizeInListValues(r)
	case *parser.FunctionFrom:
		if calls := b.normalizeInListCalls(r.Calls); calls != nil {
			next := *r
			next.Calls = calls
			return &next
		}
	case *parser.FunctionTable:
		if calls := b.normalizeInListCalls(r.Calls); calls != nil {
			next := *r
			next.Calls = calls
			return &next
		}
	}
	return raw
}

func (b *expressionBinder) normalizeInListCalls(raw []parser.FunctionExpr) []parser.FunctionExpr {
	var result []parser.FunctionExpr
	for i, call := range raw {
		if next, changed := b.normalizeInListExpression(call); changed {
			if result == nil {
				result = append([]parser.FunctionExpr(nil), raw...)
			}
			result[i] = next.(parser.FunctionExpr)
		}
	}
	return result
}
