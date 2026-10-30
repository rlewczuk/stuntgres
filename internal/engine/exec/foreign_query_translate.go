package exec

import (
	"context"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

type foreignTranslation struct {
	stage        catalog.ForeignQueryStage
	columns      []catalog.Column
	width, cache int
}

type foreignTranslator struct {
	ctx     context.Context
	input   ForeignPlanningInput
	owner   any
	sources []catalog.ForeignQuerySource
	first   *ForeignQuerySource
	err     error
}

func foreignReference(slot int, typ catalog.ForeignQuerySlot) catalog.ForeignQueryExpression {
	return catalog.ForeignQueryExpression{Kind: "slot", Slot: slot, Type: typ}
}

func (t *foreignTranslator) expression(e expr.Expr, width int) (catalog.ForeignQueryExpression, bool) {
	value, ok, err := foreignExpression(t.ctx, t.input, e, width)
	if err != nil {
		t.err = err
	}
	return value, ok && err == nil
}

func (t *foreignTranslator) translate(root plan.Node) (foreignTranslation, bool, error) {
	var out foreignTranslation
	out.width = len(root.Schema())
	out.columns = root.Schema()
	var child plan.Node
	switch n := root.(type) {
	case *plan.SourceNode:
		if t.input.Occurrence == nil { return out, false, nil }
		occurrence := t.input.Occurrence(n.Occurrence)
		f := occurrence.Source
		if !occurrence.Eligible || occurrence.Owner == nil || f == nil || len(f.Columns) != out.width {
			return out, false, nil
		}
		if _, ok := f.Wrapper.(catalog.ForeignQueryScanProvider); !ok {
			return out, false, nil
		}
		if t.first != nil {
			a, b := t.first.Request, f.Request
			if a.ServerOID != b.ServerOID || a.MappingOID != b.MappingOID || a.HandlerOID != b.HandlerOID || a.RoleOID != b.RoleOID || a.Role != b.Role || t.owner != occurrence.Owner {
				return out, false, nil
			}
		} else {
			t.first = f
			t.owner = occurrence.Owner
		}
		out.stage.Kind = "source"
		out.stage.Occurrence = n.Occurrence
		t.sources = append(t.sources, catalog.ForeignQuerySource{Occurrence: n.Occurrence, Request: CopyForeignRequest(f.Request)})
		for i, c := range out.columns {
			slot, ok := foreignColumnSlot(c, i)
			if !ok {
				return out, false, nil
			}
			if i >= len(f.Request.Columns) {
				return out, false, nil
			}
			slot.Unused = !slices.Contains(f.Request.RequiredAttributes, f.Request.Columns[i].Attribute)
			out.stage.Slots = append(out.stage.Slots, slot)
			value := foreignReference(i, slot)
			if slot.Unused {
				value = catalog.ForeignQueryExpression{Kind: "literal", Type: slot}
			}
			out.stage.Outputs = append(out.stage.Outputs, value)
		}
		if n.PresenceSlot != 0 {
			slot := catalog.ForeignQuerySlot{ID: len(out.stage.Slots), TypeName: "bool", TypeOID: 16, TypeModifier: -1, Presence: n.PresenceSlot}
			out.stage.Slots = append(out.stage.Slots, slot)
			out.stage.Outputs = append(out.stage.Outputs, catalog.ForeignQueryExpression{Kind: "literal", Literal: true, Type: slot})
		}
		return out, true, nil
	case *plan.JoinNode:
		if n.CorrelatedRight || len(n.Captures) != 0 {
			return out, false, nil
		}
		left, ok, err := t.translate(n.Left)
		if !ok || err != nil {
			return out, ok, err
		}
		right, ok, err := t.translate(n.Right)
		if !ok || err != nil {
			return out, ok, err
		}
		if left.cache != 0 || right.cache != 0 {
			return out, false, nil
		}
		out.stage.Kind = "join"
		switch n.Kind {
		case parser.JoinInner:
			out.stage.Join = "INNER"
		case parser.JoinLeft:
			out.stage.Join = "LEFT"
		case parser.JoinRight:
			out.stage.Join = "RIGHT"
		case parser.JoinFull:
			out.stage.Join = "FULL"
		default:
			return out, false, nil
		}
		out.stage.Children = []catalog.ForeignQueryStage{left.stage, right.stage}
		// Expressions use the ordinary join frame. Right source slots are
		// shifted over the left child's hidden presence suffix.
		shift := func(e *catalog.ForeignQueryExpression) {
			shiftForeignRight(e, n.LeftWidth, len(left.stage.Slots)-left.width)
		}
		if n.Predicate != nil {
			v, ok := t.expression(n.Predicate, n.LeftWidth+n.RightWidth)
			if !ok {
				return out, false, nil
			}
			shift(&v)
			out.stage.Predicate = &v
		}
		for i, c := range out.columns {
			typ, ok := foreignColumnSlot(c, i)
			if !ok {
				return out, false, nil
			}
			var v catalog.ForeignQueryExpression
			if i < n.LeftWidth {
				typ.Unused = left.stage.Slots[i].Unused
				v = foreignReference(i, typ)
			} else if i < n.LeftWidth+n.RightWidth {
				typ.Unused = right.stage.Slots[i-n.LeftWidth].Unused
				v = foreignReference(len(left.stage.Slots)+i-n.LeftWidth, typ)
			} else {
				var valid bool
				v, valid = t.expression(n.Merges[i-n.LeftWidth-n.RightWidth], n.LeftWidth+n.RightWidth)
				if !valid {
					return out, false, nil
				}
				shift(&v)
			}
			out.stage.Slots = append(out.stage.Slots, typ)
			out.stage.Outputs = append(out.stage.Outputs, v)
		}
		for side, c := range []foreignTranslation{left, right} {
			base := 0
			if side == 1 {
				base = len(left.stage.Slots)
			}
			for i, slot := range c.stage.Slots {
				if slot.Presence != 0 {
					slot.ID = len(out.stage.Slots)
					out.stage.Slots = append(out.stage.Slots, slot)
					out.stage.Outputs = append(out.stage.Outputs, catalog.ForeignQueryExpression{Kind: "present", Slot: base + i, Type: slot})
				}
			}
		}
		if n.PresenceSlot != 0 {
			slot := catalog.ForeignQuerySlot{ID: len(out.stage.Slots), TypeName: "bool", TypeOID: 16, TypeModifier: -1, Presence: n.PresenceSlot}
			out.stage.Slots = append(out.stage.Slots, slot)
			out.stage.Outputs = append(out.stage.Outputs, catalog.ForeignQueryExpression{Kind: "literal", Literal: true, Type: slot})
		}
		return out, true, nil
	case *plan.FilterNode:
		child = n.Child
		out.stage.Kind = "filter"
	case *plan.AggregateNode:
		child = n.Child
		out.stage.Kind = "aggregate"
	case *plan.SortNode:
		if n.PresenceOnly {
			return out, false, nil
		}
		child = n.Child
		out.stage.Kind = "order"
	case *plan.LimitNode:
		if n.WithTies || n.PresenceOnly {
			return out, false, nil
		}
		child = n.Child
		out.stage.Kind = "limit"
	case *plan.EvaluationNode:
		child = n.Child
		out.stage.Kind = "evaluation"
	default:
		return out, false, nil
	}
	input, ok, err := t.translate(child)
	if !ok || err != nil {
		return out, ok, err
	}
	out.stage.Children = []catalog.ForeignQueryStage{input.stage}
	out.columns = append([]catalog.Column{}, input.columns...)
	out.width, out.cache = input.width, input.cache
	for i, slot := range input.stage.Slots {
		out.stage.Outputs = append(out.stage.Outputs, foreignReference(i, slot))
	}
	out.stage.Slots = append([]catalog.ForeignQuerySlot{}, input.stage.Slots...)
	switch n := root.(type) {
	case *plan.FilterNode:
		v, ok := t.expression(n.Predicate, input.width)
		if !ok {
			return out, false, nil
		}
		out.stage.Predicate = &v
	case *plan.AggregateNode:
		if n.GroupingActive || n.PresenceOnly {
			return out, false, nil
		}
		// Ordinary GROUP BY also has one canonical grouping set. Multiple,
		// partial or repeated sets require semantics this path does not offer.
		if n.Sets != nil {
			if len(n.Sets) != 1 || len(n.Sets[0]) != len(n.Keys) {
				return out, false, nil
			}
			for i, key := range n.Sets[0] {
				if key != i {
					return out, false, nil
				}
			}
		}
		if len(n.Grouping) != len(n.Keys) {
			return out, false, nil
		}
		for _, group := range n.Grouping {
			p := group.Procedure
			if group.PrivateTree || group.Convert || group.RoutineRecipe != nil || p == nil || p.Builtin == nil || p.Builtin.Identity.Namespace != 11 || p.Builtin.Identity.ID != group.Routine.Address.ObjectID || p.Address() != group.Routine.Address || p.Volatility != expr.Immutable || p.Builtin.Volatility != expr.Immutable {
				return out, false, nil
			}
		}
		out.stage.Outputs = nil
		out.stage.Slots = nil
		out.columns = root.Schema()
		out.width = len(out.columns)
		out.cache = 0
		for _, key := range n.Keys {
			if !foreignComparable(key) {
				return out, false, nil
			}
			v, ok := t.expression(key, input.width)
			if !ok {
				return out, false, nil
			}
			out.stage.Keys = append(out.stage.Keys, v)
		}
		for _, call := range n.Calls {
			procedure := call.Procedure
			if procedure == nil || procedure.Builtin == nil || procedure.Builtin.Identity.Namespace != 11 || procedure.Builtin.Identity.ID != call.Routine.Address.ObjectID || procedure.Address() != call.Routine.Address || procedure.Volatility != expr.Immutable || procedure.Builtin.Volatility != expr.Immutable || call.RoutineRecipe != nil || len(call.Direct) != 0 || len(call.Order) != 0 || len(call.Distinct) != 0 || call.Filter != nil {
				return out, false, nil
			}
			a := catalog.ForeignQueryAggregate{Namespace: "pg_catalog", RoutineOID: uint32(call.Routine.Address.ObjectID)}
			switch call.Operation {
			case functions.AggregateCount:
				a.Name = "count"
			case functions.AggregateIntegralSum:
				a.Name = "sum"
			default:
				return out, false, nil
			}
			if procedure.Builtin.Identity.Name != a.Name {
				return out, false, nil
			}
			for _, arg := range call.Arguments {
				id := arg.ResultUsage().Type.ID()
				if id != 21 && id != 23 && id != 20 {
					return out, false, nil
				}
				v, ok := t.expression(arg, input.width)
				if !ok {
					return out, false, nil
				}
				a.Arguments = append(a.Arguments, v)
			}
			out.stage.Aggregates = append(out.stage.Aggregates, a)
		}
		for i, c := range out.columns {
			slot, ok := foreignColumnSlot(c, i)
			if !ok {
				return out, false, nil
			}
			out.stage.Slots = append(out.stage.Slots, slot)
			if i < len(n.Keys)+len(n.Calls) {
				out.stage.Outputs = append(out.stage.Outputs, foreignReference(i, slot))
			} else if i == len(n.Keys)+len(n.Calls) && slot.TypeOID == 23 {
				// The immutable aggregate frame retains the grouping-set
				// occurrence even when its sole ordinary set is occurrence zero.
				out.stage.Outputs = append(out.stage.Outputs, catalog.ForeignQueryExpression{Kind: "literal", Literal: int64(0), Type: slot})
			} else {
				return out, false, nil
			}
		}
	case *plan.SortNode:
		out.cache = n.CacheWidth
		cache := make([]catalog.ForeignQueryExpression, n.CacheWidth)
		cacheColumns := make([]catalog.Column, n.CacheWidth)
		for _, key := range n.Keys {
			if key.Ordering != nil || !foreignComparable(key.Expression) {
				return out, false, nil
			}
			v, ok := t.expression(key.Expression, input.width)
			if !ok {
				return out, false, nil
			}
			output := key.Slot
			if key.Cache >= 0 {
				output = input.width + key.Cache
			}
			if output < 0 {
				return out, false, nil
			}
			out.stage.Order = append(out.stage.Order, catalog.ForeignQueryOrder{Expression: v, OutputSlot: output, Descending: key.Desc, NullsFirst: key.NullsFirst})
			if key.Cache >= 0 {
				cache[key.Cache] = v
				cacheColumns[key.Cache] = catalog.Column{Usage: key.Expression.ResultUsage()}
			}
		}
		for i, e := range n.Outputs {
			v, ok := t.expression(e, input.width)
			if !ok {
				return out, false, nil
			}
			index := n.CacheWidth - len(n.Outputs) + i
			cache[index] = v
			cacheColumns[index] = catalog.Column{Usage: e.ResultUsage()}
		}
		if !out.setCache(input, cache) {
			return out, false, nil
		}
		out.columns = append(out.columns, cacheColumns...)
	case *plan.EvaluationNode:
		if n.Base != input.cache {
			return out, false, nil
		}
		cache := make([]catalog.ForeignQueryExpression, n.Base+len(n.Expressions))
		for i := range n.Base {
			cache[i] = foreignReference(input.width+i, input.stage.Slots[input.width+i])
		}
		for i, e := range n.Expressions {
			v, ok := t.expression(e, input.width)
			if !ok {
				return out, false, nil
			}
			cache[n.Base+i] = v
		}
		out.cache = len(cache)
		if !out.setCache(input, cache) {
			return out, false, nil
		}
		out.columns = append(out.columns, input.columns[input.width:]...)
		for _, e := range n.Expressions {
			out.columns = append(out.columns, catalog.Column{Usage: e.ResultUsage()})
		}
	case *plan.LimitNode:
		if !foreignBound(n.Offset) || !foreignBound(n.Count) {
			return out, false, nil
		}
		evaluation := Context{EvalStatement: EvalStatement{Context: t.ctx, Parameters: t.input.Parameters, Input: t.input.Input, Output: t.input.Output}}
		offset, _, err := queryBoundEvaluation(t.ctx, &evaluation, n.Offset, "OFFSET")
		if err != nil {
			return out, false, err
		}
		count, finite, err := queryBoundEvaluation(t.ctx, &evaluation, n.Count, "LIMIT")
		if err != nil {
			return out, false, err
		}
		out.stage.Offset = offset
		if finite {
			out.stage.Count = &count
		}
	}
	return out, true, nil
}

func shiftForeignRight(e *catalog.ForeignQueryExpression, start, delta int) {
	if e.Kind == "slot" && e.Slot >= start {
		e.Slot += delta
	}
	for i := range e.Arguments {
		shiftForeignRight(&e.Arguments[i], start, delta)
	}
}

func foreignBound(e expr.Expr) bool {
	if e == nil {
		return true
	}
	switch n := e.(type) {
	case plan.LiteralExpr, plan.ParameterExpr:
		return true
	case plan.CoercionExpr:
		return foreignBound(n.Child)
	}
	return false
}

func (out *foreignTranslation) setCache(input foreignTranslation, cache []catalog.ForeignQueryExpression) bool {
	out.stage.Slots = append([]catalog.ForeignQuerySlot{}, input.stage.Slots[:input.width]...)
	out.stage.Outputs = append([]catalog.ForeignQueryExpression{}, out.stage.Outputs[:input.width]...)
	out.columns = append([]catalog.Column{}, input.columns[:input.width]...)
	for _, v := range cache {
		if v.Kind == "" {
			return false
		}
		v.Type.ID = len(out.stage.Slots)
		out.stage.Slots = append(out.stage.Slots, v.Type)
		out.stage.Outputs = append(out.stage.Outputs, v)
	}
	for i, slot := range input.stage.Slots {
		if slot.Presence != 0 {
			slot.ID = len(out.stage.Slots)
			out.stage.Slots = append(out.stage.Slots, slot)
			out.stage.Outputs = append(out.stage.Outputs, foreignReference(i, slot))
		}
	}
	return true
}

func foreignColumnSlot(column catalog.Column, id int) (catalog.ForeignQuerySlot, bool) {
	slot, ok := foreignSlot(column.Usage, id)
	slot.Nullable, slot.NullabilityKnown = column.Nullable, column.NullabilityKnown
	slot.OriginTable, slot.OriginAttribute = column.OriginTable, column.OriginAttribute
	return slot, ok
}
