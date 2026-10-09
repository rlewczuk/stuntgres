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
	clause viewLiteralClause
	target int
	node   int
	source, row int
}

func (b *expressionBinder) captureViewLiterals(raw parser.Expr, bound BoundExpr, clause viewLiteralClause, target int) error {
	path := viewLiteralPath{clause: clause, target: target}
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
	case parser.IdentityExpr:
		if _, ok := bound.(identityExpression); ok {
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
				b.viewColumns = make(map[viewLiteralPath]int)
			}
			b.viewColumns[path] = column.slot
			return nil
		}
	case parser.AggregateExpr:
		if n, ok := bound.(aggregateExpression); ok {
			if r.Star {
				return nil
			}
			if len(r.Args) == 1 && n.slot >= 0 && n.slot < len(b.aggregates) {
				return child(r.Args[0], b.aggregates[n.slot].argument)
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
		if bound.ResultUsage().Type.IsDomain() {
			if b.viewDomainCasts == nil {
				b.viewDomainCasts = make(map[viewLiteralPath]sqltype.Usage)
			}
			b.viewDomainCasts[path] = bound.ResultUsage()
		}
		if coercion, ok := bound.(coercionExpression); ok && coercion.mode == sqltype.Explicit {
			bound = coercion.child
		}
		return child(r.Child, bound)
	case parser.RegclassExpr:
		return child(r.Child, bound)
	case parser.OperatorExpr:
		switch n := bound.(type) {
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
		if n, ok := bound.(likeEscapeExpression); ok {
			if err := child(r.Args[0], n.pattern); err != nil {
				return err
			}
			return child(r.Args[1], n.escape)
		}
		if n, ok := bound.(deparseExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(visibilityInquiryExpression); ok && len(r.Args) == 1 {
			return child(r.Args[0], n.arg)
		}
		if n, ok := bound.(literalExpression); ok && n.value.IsNull() && n.value.Type().ID() == sqltype.Bool {
			switch r.Name.Parts[len(r.Name.Parts)-1].Text {
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
	case parser.CallExpr:
		if n, ok := bound.(sequenceCallExpression); ok {
			return children(r.Args, n.args)
		}
	}
	return fmt.Errorf("stuntgres: mismatched view literal expression %T / %T", raw, bound)
}

func (v *viewDefinition) validateLiterals(catalog CatalogRead) error {
	count := 0
	columnCount, starCount := 0, 0
	validate := func(raw parser.Expr, clause viewLiteralClause, target int) error {
		ordinal := 0
		return v.validateLiteralTree(catalog, raw, viewLiteralPath{clause: clause, target: target}, &ordinal, &count, &columnCount)
	}
	for i, raw := range v.syntax.Targets {
		if _, star := raw.(parser.StarExpr); star {
			starCount++
			for _, slot := range v.stars[i] {
				root := viewFromRoot(v.query.Root)
				if slot < 0 || slot >= len(root.Schema()) {
					return fmt.Errorf("stuntgres: invalid frozen view star slot")
				}
			}
		}
		if err := validate(raw, viewTargetLiteral, i); err != nil {
			return err
		}
	}
	if err := validate(v.syntax.Where, viewWhereLiteral, 0); err != nil {
		return err
	}
	for i, raw := range v.syntax.GroupBy {
		if err := validate(raw, viewGroupLiteral, i); err != nil {
			return err
		}
	}
	if err := validate(v.syntax.Having, viewHavingLiteral, 0); err != nil {
		return err
	}
	if v.syntax.Order != nil {
		for i, key := range v.syntax.Order.Keys {
			if err := validate(key.Expr, viewOrderLiteral, i); err != nil {
				return err
			}
		}
	}
	if v.syntax.Bounds != nil {
		if err := validate(v.syntax.Bounds.Offset, viewOffsetLiteral, 0); err != nil {
			return err
		}
		if err := validate(v.syntax.Bounds.Count, viewLimitLiteral, 0); err != nil {
			return err
		}
	}
	join := 0
	values := 0
	if v.syntax.Values != nil {
		if err := v.validateValuesLiterals(catalog, v.syntax.Values, values, &count, &columnCount); err != nil {
			return err
		}
		values++
	}
	var validateFrom func(parser.FromItem) error
	validateFrom = func(item parser.FromItem) error {
		switch n := item.(type) {
		case *parser.ValuesQuery:
			ordinal := values
			values++
			return v.validateValuesLiterals(catalog, n, ordinal, &count, &columnCount)
		case *parser.AliasReference:
			return validateFrom(n.Input)
		case *parser.JoinReference:
			ordinal := join
			join++
			if err := validate(n.On, viewJoinLiteral, ordinal); err != nil {
				return err
			}
			if err := validateFrom(n.Left); err != nil {
				return err
			}
			return validateFrom(n.Right)
		}
		return nil
	}
	if err := validateFrom(v.syntax.From); err != nil {
		return err
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
	case parser.IdentityExpr:
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
			root := viewValuesAt(v.query.Root, path.source)
			if root == nil {
				return fmt.Errorf("stuntgres: missing VALUES order frame")
			}
			_, _, err := orderOrdinal(raw, len(root.Schema()))
			return err
		}
		if integer, ok := raw.(parser.LiteralExpr); ok && (path.clause == viewGroupLiteral || path.clause == viewOrderLiteral) && path.node == 0 && integer.Value.Kind == parser.IntegerValue {
			if path.clause == viewOrderLiteral {
				_, _, err := orderOrdinal(raw, len(v.query.Root.Schema()))
				return err
			}
			_, _, err := groupOrdinal(raw, len(v.query.Root.Schema()))
			return err
		}
		return fmt.Errorf("stuntgres: missing view literal occurrence")
	case parser.ColumnExpr:
		slot, ok := v.columns[path]
		root := viewFromRoot(v.query.Root)
		if path.clause == viewJoinLiteral {
			root = viewJoinAt(root, path.target)
		}
		if path.clause == viewValuesOrderLiteral {
			root = viewValuesAt(v.query.Root, path.source)
		}
		if !ok || root == nil || slot < 0 || slot >= len(root.Schema()) {
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
