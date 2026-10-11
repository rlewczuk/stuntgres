package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const maximumGroupingSets = 4096

type analyzedGroupItem struct {
	kind  parser.GroupKind
	key   int
	items []analyzedGroupItem
	pos   parser.Position
}

func (b *expressionBinder) bindGroupClause(raw *parser.GroupClause, projection []BoundExpr) ([]BoundExpr, []sqltype.Grouping, [][]int, error) {
	if raw == nil {
		return nil, nil, nil, nil
	}
	var keys []BoundExpr
	var families []sqltype.Grouping
	leaf := 0
	var bind func(parser.GroupItem) (analyzedGroupItem, error)
	bind = func(item parser.GroupItem) (analyzedGroupItem, error) {
		if err := b.ctx.Err(); err != nil {
			return analyzedGroupItem{}, err
		}
		out := analyzedGroupItem{kind: item.Kind, pos: item.Pos}
		if item.Kind != parser.GroupExpression {
			out.items = make([]analyzedGroupItem, len(item.Items))
			for i, child := range item.Items {
				next, err := bind(child)
				if err != nil {
					return out, err
				}
				out.items[i] = next
			}
			// Child resolution precedes the independent CUBE width guard.
			if item.Kind == parser.GroupCube && len(item.Items) > 12 {
				return out, expressionError("54011", "CUBE is limited to 12 elements", item.Pos)
			}
			return out, nil
		}
		ordinal, isOrdinal, err := groupOrdinal(item.Expr, len(projection))
		if err != nil {
			return out, err
		}
		var key BoundExpr
		if isOrdinal {
			key = projection[ordinal]
		} else {
			key, err = b.bind(item.Expr, expressionPolicy{context: projectionContext})
		}
		if err != nil {
			return out, err
		}
		if unknown, ok := key.(unknownOutput); ok {
			key, err = b.bind(unknown.raw, expressionPolicy{context: projectionContext})
			if err != nil {
				return out, err
			}
		}
		var operation *groupingExpression
		walkBoundExpression(key, func(node BoundExpr) {
			if n, ok := node.(groupingExpression); ok && n.frame == b.frameID() && operation == nil {
				operation = &n
			}
		})
		if operation != nil {
			return out, expressionError("42803", "grouping operations are not allowed in GROUP BY", operation.pos)
		}
		if call := aggregateOccurrence(key); call != nil && (call.frame == 0 || call.frame == b.frameID()) {
			return out, expressionError("42803", "aggregate functions are not allowed in GROUP BY", call.pos)
		}
		if call := windowOccurrence(key); call != nil && call.frame == b.frameID() {
			return out, expressionError("42P20", "window functions are not allowed in GROUP BY", call.pos)
		}
		key, err = b.finalizeProjection(key)
		if err != nil {
			return out, err
		}
		if (b.noParameters || b.materializedDefinition) && !isOrdinal {
			if err := b.captureViewLiterals(item.Expr, key, viewGroupLiteral, leaf); err != nil {
				return out, err
			}
		}
		leaf++
		for i, existing := range keys {
			if b.sameGroupExpression(key, existing) {
				out.key = i
				return out, nil
			}
		}
		family, err := sqltype.BindGrouping(key.ResultUsage())
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42883" {
				copy := *diagnostic
				copy.Message = "could not identify an equality operator for type " + b.diagnosticTypeName(key.ResultUsage().Type)
				if pos := item.Expr.SourcePosition(); pos.Character > 0 {
					copy.Position = fmt.Sprint(pos.Character)
				}
				return out, &copy
			}
			return out, err
		}
		out.key = len(keys)
		keys = append(keys, key)
		families = append(families, family)
		return out, nil
	}
	items := make([]analyzedGroupItem, len(raw.Items))
	for i, item := range raw.Items {
		next, err := bind(item)
		if err != nil {
			return nil, nil, nil, err
		}
		items[i] = next
	}
	sets, err := expandGroupingItems(b.ctx, items)
	if err != nil {
		return nil, nil, nil, err
	}
	if raw.Distinct {
		sort.Slice(sets, func(i, j int) bool { return slices.Compare(sets[i], sets[j]) < 0 })
		sets = slices.CompactFunc(sets, func(a, b []int) bool { return slices.Equal(a, b) })
	}
	return keys, families, sets, nil
}

func groupingUnion(a, b []int) []int {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 || slices.Equal(a, b) {
		return a
	}
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case j == len(b) || i < len(a) && a[i] < b[j]:
			out = append(out, a[i])
			i++
		case i == len(a) || b[j] < a[i]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return out
}

func expandGroupingItems(ctx context.Context, items []analyzedGroupItem) ([][]int, error) {
	sets := [][]int{{}}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part, err := expandGroupingItem(ctx, item)
		if err != nil {
			return nil, err
		}
		if len(part) > maximumGroupingSets/len(sets) {
			return nil, sqlError("54001", "too many grouping sets present (maximum 4096)")
		}
		product := make([][]int, 0, len(sets)*len(part))
		for _, a := range sets {
			for _, b := range part {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				product = append(product, groupingUnion(a, b))
			}
		}
		sets = product
	}
	return sets, nil
}

func expandGroupingItem(ctx context.Context, item analyzedGroupItem) ([][]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch item.kind {
	case parser.GroupExpression:
		return [][]int{{item.key}}, nil
	case parser.GroupEmpty:
		return [][]int{{}}, nil
	case parser.GroupList:
		return expandGroupingItems(ctx, item.items)
	case parser.GroupSets:
		var out [][]int
		for _, child := range item.items {
			sets, err := expandGroupingItem(ctx, child)
			if err != nil {
				return nil, err
			}
			if len(sets) > maximumGroupingSets-len(out) {
				return nil, expressionError("54001", "too many grouping sets present (maximum 4096)", item.pos)
			}
			out = append(out, sets...)
		}
		return out, nil
	case parser.GroupRollup:
		var out [][]int
		for n := len(item.items); n >= 0; n-- {
			sets, err := expandGroupingItems(ctx, item.items[:n])
			if err != nil {
				return nil, err
			}
			if len(sets) > maximumGroupingSets-len(out) {
				return nil, expressionError("54001", "too many grouping sets present (maximum 4096)", item.pos)
			}
			out = append(out, sets...)
		}
		return out, nil
	case parser.GroupCube:
		if len(item.items) > 12 {
			return nil, expressionError("54011", "CUBE is limited to 12 elements", item.pos)
		}
		var out [][]int
		units := make([]analyzedGroupItem, 0, len(item.items))
		for mask := range 1 << len(item.items) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			units = units[:0]
			for i, child := range item.items {
				if mask&(1<<i) != 0 {
					units = append(units, child)
				}
			}
			sets, err := expandGroupingItems(ctx, units)
			if err != nil {
				return nil, err
			}
			if len(sets) > maximumGroupingSets-len(out) {
				return nil, expressionError("54001", "too many grouping sets present (maximum 4096)", item.pos)
			}
			out = append(out, sets...)
		}
		return out, nil
	}
	return nil, fmt.Errorf("stuntgres: invalid grouping item")
}

// Arguments remain analyzed dependencies but are never evaluated by GROUPING.
// The immutable masks address the execution-local occurrence suffix in its owner.
type groupingExpression struct {
	expressionInfo
	args      []BoundExpr
	positions []parser.Position
	frame     uint32
	slot      int
	masks     []int32
}

func (e groupingExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	frame := e.frame
	if frame == 0 {
		frame = 1
	}
	for c != nil && (c.Scope != 1 || c.Frame != frame) {
		c = c.Outer
	}
	if c == nil || e.slot < 0 || e.slot >= len(c.Row) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid grouping frame")
	}
	occurrence, err := c.Row[e.slot].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	if occurrence < 0 || occurrence >= int64(len(e.masks)) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid grouping occurrence")
	}
	return sqltype.Integer(e.usage.Type, int64(e.masks[occurrence]))
}

func (b *expressionBinder) bindGrouping(raw parser.GroupingExpr) (BoundExpr, error) {
	if len(raw.Args) > 31 {
		return nil, expressionError("54023", "GROUPING must have fewer than 32 arguments", raw.Pos)
	}
	parent := b.argumentOwner
	b.argumentOwner = argumentWindowOwner{active: true}
	defer func() { b.argumentOwner = parent }()
	args := make([]BoundExpr, len(raw.Args))
	positions := make([]parser.Position, len(raw.Args))
	for i, arg := range raw.Args {
		child, err := b.bind(arg, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		args[i] = child
		positions[i] = parser.ExpressionStart(arg)
	}
	if err := b.argumentOwner.nestingError(); err != nil {
		return nil, err
	}
	if b.checkConsumer {
		return nil, expressionError("42803", "grouping operations are not allowed in check constraints", raw.Pos)
	}
	if b.indexPredicateConsumer {
		return nil, expressionError("42803", "grouping operations are not allowed in index predicates", raw.Pos)
	}
	if b.indexConsumer {
		return nil, expressionError("42803", "grouping operations are not allowed in index expressions", raw.Pos)
	}
	owner, index := b.argumentQueryOwner(func(visit func(BoundExpr)) {
		for _, arg := range args {
			walkQueryArgumentExpression(arg, visit)
		}
	})
	clause := b.aggregateClause
	if index >= 0 {
		clause = b.queryScopes[index].clause
	}
	var nested BoundExpr
	for _, arg := range args {
		walkQueryArgumentExpression(arg, func(node BoundExpr) {
			switch n := node.(type) {
			case aggregateExpression:
				if n.frame == owner && nested == nil {
					nested = n
				}
			case groupingExpression:
				if n.frame == owner && nested == nil {
					nested = n
				}
			}
		})
	}
	if nested != nil {
		return nil, expressionError("42803", "aggregate function calls cannot be nested", nested.SourcePosition())
	}
	if clause == "INSERT VALUES" {
		clause = "VALUES"
	}
	switch clause {
	case "SELECT", "HAVING", "ORDER BY", "window definitions":
	case "window RANGE", "window GROUPS":
		return nil, expressionError("42803", "grouping operations are not allowed in "+clause, raw.Pos)
	case "WHERE", "GROUP BY", "JOIN conditions", "LIMIT", "OFFSET", "RETURNING", "VALUES", "FILTER", "UPDATE":
		return nil, expressionError("42803", "grouping operations are not allowed in "+clause, raw.Pos)
	default:
		return nil, expressionError("42803", "grouping operations are not allowed in "+clause, raw.Pos)
	}
	if index >= 0 {
		b.queryScopes[index].groupingActive = true
	} else {
		b.groupingActive = true
	}
	usage := b.typ(sqltype.Int4).Usage()
	return groupingExpression{expressionInfo: expressionInfo{usage: usage, pos: raw.Pos, properties: ExprProperties{StageDependent: true}}, args: args, positions: positions, frame: owner, slot: -1}, nil
}

func (b *expressionBinder) rewriteGrouping(e groupingExpression, keys []BoundExpr) (BoundExpr, error) {
	if e.frame != b.frameID() {
		return e, nil
	}
	refs := make([]int, len(e.args))
	for i, arg := range e.args {
		refs[i] = -1
		_, variable := arg.(columnExpression)
		if !b.groupSubquery || variable {
			for k, key := range keys {
				if b.sameGroupExpression(arg, key) {
					refs[i] = k
					break
				}
			}
		}
		if refs[i] < 0 {
			pos := arg.SourcePosition()
			if i < len(e.positions) {
				pos = e.positions[i]
			}
			return nil, expressionError("42803", "arguments to GROUPING must be grouping expressions of the associated query level", pos)
		}
	}
	e.slot = len(keys) + len(b.aggregates)
	e.masks = make([]int32, len(b.groupSets))
	for i, set := range b.groupSets {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		for _, ref := range refs {
			e.masks[i] <<= 1
			if !slices.Contains(set, ref) {
				e.masks[i] |= 1
			}
		}
	}
	return e, nil
}
