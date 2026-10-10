package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type viewLiteralClause uint8

const (
	viewTargetLiteral viewLiteralClause = iota
	viewWhereLiteral
	viewGroupLiteral
	viewHavingLiteral
	viewJoinLiteral
	viewOrderLiteral
	viewOffsetLiteral
	viewLimitLiteral
	viewValuesCellLiteral
	viewValuesOrderLiteral
	viewValuesOffsetLiteral
	viewValuesLimitLiteral
)

// Each lowered syntax occurrence has its own structural key. BETWEEN can bind
// the same source position independently to different types in its two branches.
type viewLiteralPath struct {
	query       uint32
	clause      viewLiteralClause
	target      int
	node        int
	source, row int
}

func (b *expressionBinder) captureViewLiterals(raw parser.Expr, bound BoundExpr, clause viewLiteralClause, target int) error {
	path := viewLiteralPath{query: b.frameID(), clause: clause, target: target}
	ordinal := 0
	return b.captureViewLiteralTree(raw, bound, path, &ordinal)
}

func (b *expressionBinder) captureViewLiteralTree(raw parser.Expr, bound BoundExpr, path viewLiteralPath, ordinal *int) error {
	if raw == nil {
		return nil
	}
	path.node = *ordinal
	(*ordinal)++
	for {
		coercion, ok := bound.(coercionExpression)
		if !ok || coercion.mode == sqltype.Explicit {
			break
		}
		bound = coercion.child
	}
	child := func(raw parser.Expr, bound BoundExpr) error {
		return b.captureViewLiteralTree(raw, bound, path, ordinal)
	}
	children := func(raw []parser.Expr, bound []BoundExpr) error {
		if len(raw) != len(bound) {
			return fmt.Errorf("stuntgres: mismatched view literal arguments")
		}
		for i := range raw {
			if err := child(raw[i], bound[i]); err != nil {
				return err
			}
		}
		return nil
	}
	switch r := raw.(type) {
	case parser.SubqueryExpr:
		if n, ok := bound.(subqueryExpression); ok {
			return b.captureSubqueryLeft(r.Left, n, path, ordinal)
		}
	case parser.IdentityExpr:
		if _, ok := bound.(identityExpression); ok {
			return nil
		}
	case parser.CurrentDateTimeExpr:
		if _, ok := bound.(currentDateTimeExpression); ok {
			return nil
		}
	case parser.LiteralExpr, parser.TypedLiteralExpr:
		if _, typed := r.(parser.TypedLiteralExpr); typed {
			if coercion, ok := bound.(coercionExpression); ok && coercion.mode == sqltype.Explicit {
				if literal, ok := coercion.child.(literalExpression); ok && literal.usage.Type == coercion.usage.Type {
					// The stored query retains the executable typmod coercion.
					// Literal replay writes its raw value with the typed occurrence's
					// annotation, so ordinary binding reconstructs that same coercion.
					literal.usage = coercion.usage
					bound = literal
				}
			}
		}
		if literal, ok := bound.(literalExpression); ok {
			if b.viewLiterals == nil {
				b.viewLiterals = make(map[viewLiteralPath]literalExpression)
			}
			b.viewLiterals[path] = literal
			return nil
		}
	case parser.ColumnExpr:
		if column, ok := bound.(columnExpression); ok {
			if b.viewColumns == nil {
				b.viewColumns = make(map[viewLiteralPath]viewColumn)
			}
			b.viewColumns[path] = viewColumn{frame: column.frame, slot: column.slot}
			return nil
		}
	case parser.AggregateExpr:
		if n, ok := bound.(aggregateExpression); ok {
			if call, ok := b.aggregateCallAt(n); ok {
				for i, arg := range r.Args {
					expression := call.argument
					if i == 1 {
						expression = call.separator
					}
					if expression == nil || i > 1 {
						return fmt.Errorf("stuntgres: invalid stored aggregate arguments")
					}
					if err := child(arg, expression); err != nil {
						return err
					}
				}
				if len(r.Order) != len(call.order) {
					return fmt.Errorf("stuntgres: invalid stored aggregate ordering")
				}
				for i, key := range r.Order {
					if err := child(key.Expr, call.order[i].expression); err != nil {
						return err
					}
				}
				if r.Filter != nil {
					if err := child(r.Filter, call.filter); err != nil {
						return err
					}
				}
				return nil
			}
		}
	case parser.TimeZoneExpr:
		if n, ok := bound.(timeZoneExpression); ok && r.Local == n.local {
			if err := child(r.Value, n.value); err != nil {
				return err
			}
			if !r.Local {
				return child(r.Zone, n.zone)
			}
			return nil
		}
	case parser.CollateExpr:
		if n, ok := bound.(collationExpression); ok {
			return child(r.Child, n.child)
		}
	case parser.CastExpr:
		if b.viewCastUsages == nil {
			b.viewCastUsages = make(map[viewLiteralPath]sqltype.Usage)
		}
		b.viewCastUsages[path] = bound.ResultUsage()
		if coercion, ok := bound.(coercionExpression); ok && coercion.mode == sqltype.Explicit {
			bound = coercion.child
		}
		return child(r.Child, bound)
	case parser.RegclassExpr:
		return child(r.Child, bound)
	case parser.OperatorExpr:
		switch n := bound.(type) {
		case subqueryExpression:
			if len(r.Args) != 2 {
				return fmt.Errorf("stuntgres: mismatched row subquery syntax")
			}
			if err := b.captureSubqueryLeft(r.Args[0], n, path, ordinal); err != nil {
				return err
			}
			return child(r.Args[1], n)
		case binaryExpression:
			return children(r.Args, n.args)
		case literalExpression:
			// Unary plus over a checked integer or numeric literal is identity.
			// Keep the captured value at the operand occurrence, matching the
			// syntax traversal used by validation and reconstruction.
			if len(r.Name.Parts) == 1 && r.Name.Parts[0].Text == "+" && len(r.Args) == 1 {
				if operand, ok := r.Args[0].(parser.LiteralExpr); ok && (operand.Value.Kind == parser.NumericValue || operand.Value.Kind == parser.IntegerValue) {
					return child(operand, n)
				}
			}
		case datetimeFormattingExpression:
			return children(r.Args, n.args[:])
		case stringExpression:
			return children(r.Args, n.args)
		case bitwiseExpression:
			if err := child(r.Args[0], n.left); err != nil {
				return err
			}
			if len(r.Args) == 2 {
				return child(r.Args[1], n.right)
			}
			return nil
		case arithmeticExpression:
			if err := child(r.Args[0], n.left); err != nil {
				return err
			}
			if len(r.Args) == 2 {
				return child(r.Args[1], n.right)
			}
			return nil
		case likeExpression:
			if err := child(r.Args[0], n.left); err != nil {
				return err
			}
			return child(r.Args[1], n.right)
		case comparisonExpression:
			if err := child(r.Args[0], n.left); err != nil {
				return err
			}
			return child(r.Args[1], n.right)
		}
	case parser.LogicalExpr:
		if n, ok := bound.(logicalExpression); ok {
			if err := child(r.Left, n.left); err != nil {
				return err
			}
			return child(r.Right, n.right)
		}
	case parser.PredicateExpr:
		switch n := bound.(type) {
		case nullTestExpression:
			return child(r.Left, n.child)
		case truthTestExpression:
			return child(r.Left, n.child)
		case distinctExpression:
			if err := child(r.Left, n.left); err != nil {
				return err
			}
			return child(r.Right, n.right)
		}
	case parser.ConditionalExpr:
		if n, ok := bound.(conditionalExpression); ok {
			return children(r.Args, n.args)
		}
	case parser.CaseExpr:
		if n, ok := bound.(caseExpression); ok && len(r.Arms) == len(n.arms) {
			if err := child(r.Test, n.test); err != nil {
				return err
			}
			for i, arm := range r.Arms {
				if err := child(arm.Condition, n.arms[i].condition); err != nil {
					return err
				}
				if err := child(arm.Result, n.arms[i].result); err != nil {
					return err
				}
			}
			return child(r.Else, n.otherwise)
		}
	case parser.FunctionExpr:
		if _, ok := bound.(identityExpression); ok && len(r.Args) == 0 {
			return nil
		}
		if n, ok := bound.(randomExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(likeEscapeExpression); ok {
			if err := child(r.Args[0], n.pattern); err != nil {
				return err
			}
			return child(r.Args[1], n.escape)
		}
		if n, ok := bound.(indexPropertyExpression); ok {
			return children(r.Args, n.children())
		}
		if n, ok := bound.(deparseExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(visibilityInquiryExpression); ok && len(r.Args) == 1 {
			return child(r.Args[0], n.arg)
		}
		if n, ok := bound.(literalExpression); ok && n.value.IsNull() && n.value.Type().ID() == sqltype.Bool {
			switch r.Name.Parts[len(r.Name.Parts)-1].Text {
			case "pg_indexam_has_property", "pg_index_has_property", "pg_index_column_has_property":
				node, err := b.bindIndexPropertyStructure(r, false)
				if err != nil {
					return err
				}
				return children(r.Args, node.(indexPropertyExpression).children())
			case "pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible":
				structure, err := b.bindVisibilityInquiryStructure(r)
				if err != nil {
					return err
				}
				return child(r.Args[0], structure.arg)
			}
		}
		if n, ok := bound.(commentInquiryExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(literalExpression); ok && n.value.IsNull() && n.value.Type().ID() == sqltype.Text {
			switch r.Name.Parts[len(r.Name.Parts)-1].Text {
			case "col_description", "obj_description", "shobj_description":
				visible, err := b.builtinVisible(r.Name, functionLookup)
				if err != nil {
					return err
				}
				// Reconstruct only for typed literal/column capture. Keep the
				// executable NULL fold, with no child evaluation or preflight.
				structure, err := b.bindCommentInquiryStructure(r, visible)
				if err != nil {
					return err
				}
				return children(r.Args, structure.args)
			}
		}
		if n, ok := bound.(binaryExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(datetimeMakeExpression); ok {
			if len(r.Args) != n.writtenCount {
				return fmt.Errorf("stuntgres: mismatched constructor syntax")
			}
			for i, arg := range r.Args {
				if err := child(arg, n.args[n.writtenSlots[i]]); err != nil {
					return err
				}
			}
			return nil
		}
		if n, ok := bound.(datetimeFormattingExpression); ok {
			return children(r.Args, n.args[:])
		}
		if n, ok := bound.(stringExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(mathExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(currentSchemasExpression); ok && len(r.Args) == 1 {
			return child(r.Args[0], n.arg)
		}
		if n, ok := bound.(nullCountExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(formatExpression); ok {
			return children(r.Args, n.args)
		}
	case parser.CallExpr:
		if n, ok := bound.(sequenceCallExpression); ok {
			return children(r.Args, n.args)
		}
	}
	return fmt.Errorf("stuntgres: mismatched view literal expression %T / %T", raw, bound)
}

func (v *viewDefinition) validateLiterals(catalog CatalogRead) error {
	count, columnCount, starCount := 0, 0, 0
	for frame, project := range v.frames {
		s := project.syntax
		if s == nil {
			return fmt.Errorf("stuntgres: missing stored query syntax")
		}
		validate := func(raw parser.Expr, clause viewLiteralClause, target int) error {
			ordinal := 0
			return v.validateLiteralTree(catalog, raw, viewLiteralPath{query: frame, clause: clause, target: target}, &ordinal, &count, &columnCount)
		}
		for i, raw := range s.Targets {
			if _, star := raw.(parser.StarExpr); star {
				starCount++
				path := viewLiteralPath{query: frame, clause: viewTargetLiteral, target: i}
				columns, ok := v.stars[path]
				if !ok {
					return fmt.Errorf("stuntgres: missing frozen view star")
				}
				for _, column := range columns {
					owner := v.frames[column.frame]
					if owner == nil {
						return fmt.Errorf("stuntgres: missing frozen star frame")
					}
					root := viewFromRoot(owner)
					if column.slot < 0 || column.slot >= len(root.Schema()) {
						return fmt.Errorf("stuntgres: invalid frozen view star slot")
					}
				}
			}
			if err := validate(raw, viewTargetLiteral, i); err != nil {
				return err
			}
		}
		if err := validate(s.Where, viewWhereLiteral, 0); err != nil {
			return err
		}
		for i, raw := range s.GroupBy {
			if err := validate(raw, viewGroupLiteral, i); err != nil {
				return err
			}
		}
		if err := validate(s.Having, viewHavingLiteral, 0); err != nil {
			return err
		}
		if s.Order != nil {
			for i, key := range s.Order.Keys {
				if err := validate(key.Expr, viewOrderLiteral, i); err != nil {
					return err
				}
			}
		}
		if s.Bounds != nil {
			if err := validate(s.Bounds.Offset, viewOffsetLiteral, 0); err != nil {
				return err
			}
			if err := validate(s.Bounds.Count, viewLimitLiteral, 0); err != nil {
				return err
			}
		}
		var from func(parser.FromItem, LogicalNode) error
		from = func(item parser.FromItem, node LogicalNode) error {
			switch raw := item.(type) {
			case *parser.AliasReference:
				return from(raw.Input, node)
			case *parser.JoinReference:
				join, ok := node.(*joinNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored join frame")
				}
				if err := validate(raw.On, viewJoinLiteral, join.viewOrdinal); err != nil {
					return err
				}
				if err := from(raw.Left, join.left); err != nil {
					return err
				}
				return from(raw.Right, join.right)
			case *parser.ValuesQuery:
				values, ok := viewFromRoot(node).(*valuesNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored VALUES frame")
				}
				return v.validateValuesLiterals(catalog, raw, frame, values.viewOrdinal, &count, &columnCount)
			}
			return nil
		}
		if s.Values != nil {
			if err := from(s.Values, viewFromRoot(project)); err != nil {
				return err
			}
		} else if err := from(s.From, viewFromRoot(project)); err != nil {
			return err
		}
	}
	if count != len(v.literals) || columnCount != len(v.columns) || starCount != len(v.stars) {
		return fmt.Errorf("stuntgres: invalid view literal occurrence path")
	}
	return nil
}

func (v *viewDefinition) validateLiteralTree(catalog CatalogRead, raw parser.Expr, path viewLiteralPath, ordinal, count, columnCount *int) error {
	if raw == nil {
		return nil
	}
	path.node = *ordinal
	(*ordinal)++
	child := func(raw parser.Expr) error {
		return v.validateLiteralTree(catalog, raw, path, ordinal, count, columnCount)
	}
	args := func(raw []parser.Expr) error {
		for _, arg := range raw {
			if err := child(arg); err != nil {
				return err
			}
		}
		return nil
	}
	switch r := raw.(type) {
	case parser.SubqueryExpr:
		if v.queries[r.Query] == nil {
			return fmt.Errorf("stuntgres: missing frozen subquery syntax")
		}
		return child(r.Left)
	case parser.RowExpr:
		return args(r.Fields)
	case parser.IdentityExpr:
		return nil
	case parser.CurrentDateTimeExpr:
		return nil
	case parser.LiteralExpr, parser.TypedLiteralExpr:
		if literal, ok := v.literals[path]; ok {
			usage := literal.ResultUsage()
			if !usage.Valid() || literal.value.Type() != usage.Type {
				return fmt.Errorf("stuntgres: invalid view literal usage")
			}
			typ, ok := catalog.LookupType(usage.Type.ID())
			if !ok || typ != usage.Type {
				return fmt.Errorf("stuntgres: foreign view literal type")
			}
			(*count)++
			return nil
		}
		if integer, ok := raw.(parser.LiteralExpr); ok && path.clause == viewValuesOrderLiteral && path.node == 0 && integer.Value.Kind == parser.IntegerValue {
			root := viewValuesAt(v.frames[path.query], path.source)
			if root == nil {
				return fmt.Errorf("stuntgres: missing VALUES order frame")
			}
			_, _, err := orderOrdinal(raw, len(root.Schema()))
			return err
		}
		if integer, ok := raw.(parser.LiteralExpr); ok && (path.clause == viewGroupLiteral || path.clause == viewOrderLiteral) && path.node == 0 && integer.Value.Kind == parser.IntegerValue {
			if path.clause == viewOrderLiteral {
				_, _, err := orderOrdinal(raw, len(v.frames[path.query].Schema()))
				return err
			}
			_, _, err := groupOrdinal(raw, len(v.frames[path.query].Schema()))
			return err
		}
		return fmt.Errorf("stuntgres: missing view literal occurrence")
	case parser.ColumnExpr:
		column, ok := v.columns[path]
		owner := v.frames[column.frame]
		if !ok || owner == nil {
			return fmt.Errorf("stuntgres: invalid view column frame")
		}
		root := viewFromRoot(owner)
		if path.clause == viewJoinLiteral && column.frame == path.query {
			root = viewJoinAt(root, path.target)
		}
		if path.clause == viewValuesOrderLiteral && column.frame == path.query {
			root = viewValuesAt(owner, path.source)
		}
		if root == nil || column.slot < 0 || column.slot >= len(root.Schema()) {
			return fmt.Errorf("stuntgres: invalid view column occurrence")
		}
		(*columnCount)++
		return nil
	case parser.StarExpr:
		return nil
	case parser.AggregateExpr:
		for _, arg := range r.Args {
			if err := child(arg); err != nil {
				return err
			}
		}
		for _, key := range r.Order {
			if err := child(key.Expr); err != nil {
				return err
			}
		}
		if r.Filter != nil {
			return child(r.Filter)
		}
		return nil
	case parser.TimeZoneExpr:
		if err := child(r.Value); err != nil {
			return err
		}
		if !r.Local {
			return child(r.Zone)
		}
		return nil
	case parser.CollateExpr:
		return child(r.Child)
	case parser.CastExpr:
		return child(r.Child)
	case parser.RegclassExpr:
		return child(r.Child)
	case parser.OperatorExpr:
		return args(r.Args)
	case parser.LogicalExpr:
		if err := child(r.Left); err != nil {
			return err
		}
		return child(r.Right)
	case parser.PredicateExpr:
		if err := child(r.Left); err != nil {
			return err
		}
		return child(r.Right)
	case parser.ConditionalExpr:
		return args(r.Args)
	case parser.CaseExpr:
		if err := child(r.Test); err != nil {
			return err
		}
		for _, arm := range r.Arms {
			if err := child(arm.Condition); err != nil {
				return err
			}
			if err := child(arm.Result); err != nil {
				return err
			}
		}
		return child(r.Else)
	case parser.FunctionExpr:
		return args(r.Args)
	case parser.CallExpr:
		return args(r.Args)
	}
	return fmt.Errorf("stuntgres: invalid view literal syntax %T", raw)
}

func (b *expressionBinder) captureSubqueryLeft(raw parser.Expr, n subqueryExpression, path viewLiteralPath, ordinal *int) error {
	if raw == nil {
		return nil
	}
	if row, ok := raw.(parser.RowExpr); ok {
		(*ordinal)++
		if len(row.Fields) != len(n.comparisons) {
			return fmt.Errorf("stuntgres: mismatched frozen row comparison")
		}
		for i, field := range row.Fields {
			if err := b.captureViewLiteralTree(field, n.comparisons[i].left, path, ordinal); err != nil {
				return err
			}
		}
		return nil
	}
	if len(n.comparisons) != 1 {
		return fmt.Errorf("stuntgres: mismatched frozen scalar comparison")
	}
	return b.captureViewLiteralTree(raw, n.comparisons[0].left, path, ordinal)
}
