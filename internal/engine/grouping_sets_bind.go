package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
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

func (b *expressionBinder) bindGroupClause(raw *parser.GroupClause, projection []expr.Expr, names []string) ([]expr.Expr, []plan.Grouping, [][]int, error) {
	if raw == nil {
		return nil, nil, nil, nil
	}
	var keys []expr.Expr
	var families []plan.Grouping
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
				return out, expr.ErrorAt("54011", "CUBE is limited to 12 elements", item.Pos)
			}
			return out, nil
		}
		ordinal, isOrdinal, err := groupOrdinal(item.Expr, len(projection))
		if err != nil {
			return out, err
		}
		if !isOrdinal {
			if column, ok := item.Expr.(parser.ColumnExpr); ok && len(column.Name.Parts) == 1 {
				name := column.Name.Parts[0].Text
				input := false
				for _, candidate := range b.query.namespace.Columns {
					input = input || candidate.Descriptor.Name == name
				}
				if !input {
					output := -1
					for i, label := range names {
						if label != name {
							continue
						}
						if output >= 0 && !b.sameQueryTarget(projection[output], projection[i]) {
							return out, expr.ErrorAt("42702", fmt.Sprintf(`GROUP BY "%s" is ambiguous`, name), column.Pos)
						}
						output = i
					}
					if output >= 0 {
						ordinal, isOrdinal = output, true
					}
				}
			}
		}
		var key expr.Expr
		if isOrdinal {
			key = projection[ordinal]
		} else {
			key, err = b.bind(item.Expr, expressionPolicy{context: projectionContext})
			// GROUP BY prefers an input column, unlike ORDER BY. Only a
			// missing bare column can fall back to a SELECT output alias.
			if column, ok := item.Expr.(parser.ColumnExpr); ok && len(column.Name.Parts) == 1 {
				if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42703" {
					output := -1
					for i, name := range names {
						if name != column.Name.Parts[0].Text {
							continue
						}
						if output >= 0 && !b.sameQueryTarget(projection[output], projection[i]) {
							return out, expr.ErrorAt("42702", fmt.Sprintf(`GROUP BY "%s" is ambiguous`, name), column.Pos)
						}
						output = i
					}
					if output >= 0 {
						key, err = projection[output], nil
					}
				}
			}
		}
		if err != nil {
			return out, err
		}
		if unknown, ok := key.(plan.UnknownOutput); ok {
			key, err = b.bind(unknown.Raw, expressionPolicy{context: projectionContext})
			if err != nil {
				return out, err
			}
		}
		var operation *plan.GroupingExpr
		plan.WalkExpression(key, func(node expr.Expr) {
			if n, ok := node.(plan.GroupingExpr); ok && n.Frame == b.frameID() && operation == nil {
				operation = &n
			}
		})
		if operation != nil {
			return out, expr.ErrorAt("42803", "grouping operations are not allowed in GROUP BY", operation.Pos)
		}
		if call := aggregateOccurrence(key); call != nil && (call.Frame == 0 || call.Frame == b.frameID()) {
			return out, expr.ErrorAt("42803", "aggregate functions are not allowed in GROUP BY", call.Pos)
		}
		if call := windowOccurrence(key); call != nil && call.Frame == b.frameID() {
			return out, expr.ErrorAt("42P20", "window functions are not allowed in GROUP BY", call.Pos)
		}
		key, err = b.finalizeProjection(key)
		if err != nil {
			return out, err
		}
		if (b.params.noParameters || b.consumer.materializedDefinition) && !isOrdinal {
			if err := b.captureViewLiterals(item.Expr, key, analyze.ViewGroupLiteral, leaf); err != nil {
				return out, err
			}
		}
		leaf++
		// PostgreSQL reuses an equivalent SELECT target, including its
		// collation provenance, rather than the later GROUP BY spelling.
		for _, target := range projection {
			if b.sameGroupExpression(key, target) {
				key = target
				break
			}
		}
		for i, existing := range keys {
			if b.sameGroupExpression(key, existing) {
				out.key = i
				return out, nil
			}
		}
		family, err := b.bindDistinctGrouping(key)
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
				return nil, expr.ErrorAt("54001", "too many grouping sets present (maximum 4096)", item.pos)
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
				return nil, expr.ErrorAt("54001", "too many grouping sets present (maximum 4096)", item.pos)
			}
			out = append(out, sets...)
		}
		return out, nil
	case parser.GroupCube:
		if len(item.items) > 12 {
			return nil, expr.ErrorAt("54011", "CUBE is limited to 12 elements", item.pos)
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
				return nil, expr.ErrorAt("54001", "too many grouping sets present (maximum 4096)", item.pos)
			}
			out = append(out, sets...)
		}
		return out, nil
	}
	return nil, fmt.Errorf("stuntgres: invalid grouping item")
}

func (b *expressionBinder) bindGrouping(raw parser.GroupingExpr) (expr.Expr, error) {
	if len(raw.Args) > 31 {
		return nil, expr.ErrorAt("54023", "GROUPING must have fewer than 32 arguments", raw.Pos)
	}
	parent := b.grouping.argumentOwner
	b.grouping.argumentOwner = argumentWindowOwner{active: true}
	defer func() { b.grouping.argumentOwner = parent }()
	args := make([]expr.Expr, len(raw.Args))
	positions := make([]parser.Position, len(raw.Args))
	for i, arg := range raw.Args {
		child, err := b.bind(arg, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		args[i] = child
		positions[i] = parser.ExpressionStart(arg)
	}
	if err := b.grouping.argumentOwner.nestingError(); err != nil {
		return nil, err
	}
	if b.consumer.checkConsumer {
		return nil, expr.ErrorAt("42803", "grouping operations are not allowed in check constraints", raw.Pos)
	}
	if b.consumer.indexPredicateConsumer {
		return nil, expr.ErrorAt("42803", "grouping operations are not allowed in index predicates", raw.Pos)
	}
	if b.consumer.indexConsumer {
		return nil, expr.ErrorAt("42803", "grouping operations are not allowed in index expressions", raw.Pos)
	}
	owner, index := b.argumentQueryOwner(func(visit func(expr.Expr)) {
		for _, arg := range args {
			walkQueryArgumentExpression(arg, visit)
		}
	})
	clause := b.clause.aggregateClause
	if index >= 0 {
		clause = b.query.scopes[index].clause
	}
	var nested expr.Expr
	for _, arg := range args {
		walkQueryArgumentExpression(arg, func(node expr.Expr) {
			switch n := node.(type) {
			case plan.AggregateExpr:
				if n.Frame == owner && nested == nil {
					nested = n
				}
			case plan.GroupingExpr:
				if n.Frame == owner && nested == nil {
					nested = n
				}
			}
		})
	}
	if nested != nil {
		return nil, expr.ErrorAt("42803", "aggregate function calls cannot be nested", nested.SourcePosition())
	}
	if clause == "INSERT VALUES" {
		clause = "VALUES"
	}
	switch clause {
	case "SELECT", "HAVING", "ORDER BY", "window definitions":
	case "window RANGE", "window GROUPS", "window ROWS":
		return nil, expr.ErrorAt("42803", "grouping operations are not allowed in "+clause, raw.Pos)
	case "WHERE", "GROUP BY", "JOIN conditions", "LIMIT", "OFFSET", "RETURNING", "VALUES", "FILTER", "UPDATE":
		return nil, expr.ErrorAt("42803", "grouping operations are not allowed in "+clause, raw.Pos)
	default:
		return nil, expr.ErrorAt("42803", "grouping operations are not allowed in "+clause, raw.Pos)
	}
	if index >= 0 {
		b.query.scopes[index].groupingActive = true
	} else {
		b.grouping.active = true
	}
	usage := b.typ(sqltype.Int4).Usage()
	return plan.GroupingExpr{Info: expr.Info{Usage: usage, Pos: raw.Pos, Property: expr.Properties{StageDependent: true}}, Args: args, Positions: positions, Frame: owner, Slot: -1}, nil
}

func (b *expressionBinder) rewriteGrouping(e plan.GroupingExpr, keys []expr.Expr) (expr.Expr, error) {
	if e.Frame != b.frameID() {
		return e, nil
	}
	refs := make([]int, len(e.Args))
	for i, arg := range e.Args {
		refs[i] = -1
		_, variable := arg.(plan.ColumnExpr)
		if !b.grouping.groupSubquery || variable {
			for k, key := range keys {
				if b.sameGroupExpression(arg, key) {
					refs[i] = k
					break
				}
			}
		}
		if refs[i] < 0 {
			pos := arg.SourcePosition()
			if i < len(e.Positions) {
				pos = e.Positions[i]
			}
			return nil, expr.ErrorAt("42803", "arguments to GROUPING must be grouping expressions of the associated query level", pos)
		}
	}
	e.Slot = len(keys) + len(b.grouping.aggregates)
	e.Masks = make([]int32, len(b.grouping.groupSets))
	for i, set := range b.grouping.groupSets {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		for _, ref := range refs {
			e.Masks[i] <<= 1
			if !slices.Contains(set, ref) {
				e.Masks[i] |= 1
			}
		}
	}
	return e, nil
}
