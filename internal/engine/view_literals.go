package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
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
	viewWindowLiteral
	viewDistinctOnLiteral
	viewFunctionLiteral
)

// Each lowered syntax occurrence has its own structural key. BETWEEN can bind
// the same source position independently to different types in its two branches.
type viewLiteralPath struct {
	query       uint32
	clause      viewLiteralClause
	target      int
	node        int
	source, row int
	// Zero names the raw occurrence; positive values name retained implicit
	// coercions of a selected parent's arguments without consuming raw ordinals.
	argument int
}

func (b *expressionBinder) captureViewLiterals(raw parser.Expr, bound BoundExpr, clause viewLiteralClause, target int) error {
	path := viewLiteralPath{query: b.frameID(), clause: clause, target: target}
	ordinal := 0
	return b.captureViewLiteralTree(raw, bound, path, &ordinal)
}

func (b *expressionBinder) captureViewLiteralTree(raw parser.Expr, bound BoundExpr, path viewLiteralPath, ordinal *int) error {
	return b.captureNormalizedViewLiteralTree(b.normalizeStoredExpression(raw), bound, path, ordinal)
}

func (b *expressionBinder) captureNormalizedViewLiteralTree(raw parser.Expr, bound BoundExpr, path viewLiteralPath, ordinal *int) error {
	if raw == nil {
		return nil
	}
	path.node = *ordinal
	(*ordinal)++
	// Contextual ARRAY/ROW member casts have an executable plan but no raw
	// member CastExpr. Their enclosing constructor owns the annotation.
	for {
		coercion, ok := bound.(coercionExpression)
		if !ok || coercion.mode == sqltype.Explicit && !coercion.syntheticConstructorElement {
			break
		}
		if constructor, ok := raw.(parser.SQLJSONExpr); ok && constructor.Returning != nil && coercion.mode == sqltype.Assignment {
			break
		}
		if aggregate, ok := raw.(parser.AggregateExpr); ok && aggregate.JSON != nil && aggregate.JSON.Returning != nil && coercion.mode == sqltype.Assignment {
			break // RETURNING owns this assignment conversion in the raw syntax.
		}
		bound = coercion.child
	}
	child := func(raw parser.Expr, bound BoundExpr) error {
		childPath := path
		childPath.argument = 0
		return b.captureNormalizedViewLiteralTree(raw, bound, childPath, ordinal)
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
	selectedArrayArguments := func(raw []parser.Expr, bound []BoundExpr) error {
		if len(raw) != len(bound) {
			return fmt.Errorf("stuntgres: mismatched selected array arguments")
		}
		for i, arg := range bound {
			if coercion, ok := arg.(coercionExpression); ok && coercion.mode == sqltype.Implicit {
				if b.viewCastUsages == nil {
					b.viewCastUsages = make(map[viewLiteralPath]sqltype.Usage)
				}
				key := path
				key.argument = i + 1
				b.viewCastUsages[key] = coercion.usage
			}
			if err := child(raw[i], arg); err != nil {
				return err
			}
		}
		return nil
	}
	switch r := raw.(type) {
	case parser.ParameterExpr:
		// MV parameter admission follows complete typed analysis. No such
		// definition can publish; leave this occurrence to the later 0A000.
		if parameter, ok := bound.(parameterExpression); b.materializedDefinition && ok && parameter.number == r.Number {
			return nil
		}
	case parser.ArrayComparisonExpr:
		if n, ok := bound.(scalarArrayComparisonExpression); ok {
			if err := child(r.Left, n.left); err != nil {
				return err
			}
			if n.array == nil {
				// A homogeneous IN batch has synthetic frozen ARRAY syntax,
				// but no array construction in its executable representation.
				return child(r.Right, arrayExpression{elements: n.items})
			}
			return child(r.Right, n.array)
		}
	case parser.SQLJSONExpr:
		if r.Returning != nil {
			if b.viewCastUsages == nil {
				b.viewCastUsages = make(map[viewLiteralPath]sqltype.Usage)
			}
			b.viewCastUsages[path] = bound.ResultUsage()
			if c, ok := bound.(coercionExpression); ok {
				bound = c.child
			}
		}
		if r.Query != nil {
			if n, ok := bound.(subqueryExpression); ok {
				return b.captureSubqueryLeft(nil, n, path, ordinal)
			}
		} else if n, ok := bound.(sqlJSONExpression); ok {
			for i, arg := range r.Args {
				if err := child(arg, sqlJSONSource(n.args[i])); err != nil {
					return err
				}
			}
			return nil
		}
	case parser.JSONPredicateExpr:
		if n, ok := bound.(jsonPredicateExpression); ok {
			return child(r.Child, n.child)
		}
	case parser.SubqueryExpr:
		if n, ok := bound.(subqueryExpression); ok {
			return b.captureSubqueryLeft(r.Left, n, path, ordinal)
		}
	case parser.IdentityExpr:
		if _, ok := bound.(identityExpression); ok {
			return nil
		}
	case parser.CurrentDateTimeExpr:
		if _, ok := bound.(functions.CurrentDateTime[*EvalContext, ExprProperties]); ok {
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
		if _, plain := r.(parser.LiteralExpr); plain {
			// Explicit constructor-member coercions have no raw CastExpr.
			// Capture the literal input, retaining the executable domain/typmod
			// conversion on the stored tree and reconstructing it on replay.
			for {
				coercion, ok := bound.(coercionExpression)
				if !ok {
					break
				}
				bound = coercion.child
			}
		}
		if literal, ok := bound.(literalExpression); ok {
			if err := validateRegroleStoredLiteral(literal.value); err != nil {
				return err
			}
			if b.viewLiterals == nil {
				b.viewLiterals = make(map[viewLiteralPath]literalExpression)
			}
			b.viewLiterals[path] = literal
			return nil
		}
	case parser.ColumnExpr, parser.StarExpr:
		if column, ok := bound.(columnExpression); ok {
			if b.viewColumns == nil {
				b.viewColumns = make(map[viewLiteralPath]viewColumn)
			}
			captured := viewColumn{frame: column.frame, slot: column.slot}
			if column.scope == rulePseudoScope {
				captured.ruleRef = column.origin
				captured.ruleSide = column.frame
			}
			b.viewColumns[path] = captured
			return nil
		}
		if row, ok := bound.(rowExpression); ok && row.wholeNames != nil {
			b.captureViewComposite(path, compositeWholeRow, row, nil)
			return nil
		}
	case parser.GroupingExpr:
		if n, ok := bound.(groupingExpression); ok {
			return children(r.Args, n.args)
		}
	case parser.AggregateExpr:
		if r.JSON != nil && r.JSON.Returning != nil {
			if b.viewCastUsages == nil {
				b.viewCastUsages = make(map[viewLiteralPath]sqltype.Usage)
			}
			b.viewCastUsages[path] = bound.ResultUsage()
			if conversion, ok := bound.(coercionExpression); ok {
				bound = conversion.child
			}
		}
		if n, ok := bound.(*windowExpression); ok && r.Window != nil {
			if n.call < 0 || n.call >= len(b.windowCalls) {
				return fmt.Errorf("stuntgres: invalid stored window occurrence")
			}
			if call := b.windowCalls[n.call]; call.argument != nil {
				if err := child(r.Args[0], call.argument); err != nil {
					return err
				}
			}
			if call := b.windowCalls[n.call]; call.ordinal != nil {
				if err := child(r.Args[1], call.ordinal); err != nil {
					return err
				}
			}
			if call := b.windowCalls[n.call]; call.defaultValue != nil {
				if err := child(r.Args[2], call.defaultValue); err != nil {
					return err
				}
			}
			spec := b.windowCalls[n.call].spec
			for i, key := range r.Window.Partition {
				if err := child(key, spec.partition[i]); err != nil {
					return err
				}
			}
			for i, key := range r.Window.Order {
				if err := child(key.Expr, spec.order[spec.orderMap[i]].expression); err != nil {
					return err
				}
			}
			if r.Window.Frame != nil {
				if err := child(r.Window.Frame.Start.Offset, spec.frame.start.offset); err != nil {
					return err
				}
				if err := child(r.Window.Frame.End.Offset, spec.frame.end.offset); err != nil {
					return err
				}
			}
			return nil
		}
		if n, ok := bound.(aggregateExpression); ok {
			if call, ok := b.aggregateCallAt(n); ok {
				for i, arg := range r.Args {
					expression := call.argument
					if r.Within {
						if i >= len(call.direct) {
							return fmt.Errorf("stuntgres: invalid stored direct arguments")
						}
						expression = call.direct[i]
					} else if i == 1 {
						expression = call.separator
					} else if i > 1 {
						return fmt.Errorf("stuntgres: invalid stored aggregate arguments")
					}
					if hypotheticalOperation(call.operation) {
						expression = expression.(collationExpression).child
					}
					if conversion, ok := expression.(coercionExpression); ok && conversion.plan.IsJSONFormattedInput() {
						expression = conversion.child
					}
					if expression == nil {
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
					expression := call.order[i].expression
					if hypotheticalOperation(call.operation) {
						expression = expression.(collationExpression).child
					}
					if err := child(key.Expr, expression); err != nil {
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
		if n, ok := bound.(functions.Timezone[BoundExpr, *EvalContext, ExprProperties]); ok && r.Local == n.Local {
			if err := child(r.Value, n.Value); err != nil {
				return err
			}
			if !r.Local {
				return child(r.Zone, n.Zone)
			}
			return nil
		}
	case parser.RowExpr:
		if n, ok := bound.(rowExpression); ok {
			slot := 0
			for target, field := range r.Fields {
				if expansion, expanded := n.expansions[target]; expanded {
					if err := b.captureCompositeExpansion(field, expansion, path, ordinal); err != nil {
						return err
					}
					slot += len(expansion.fields)
				} else {
					if slot >= len(n.fields) {
						return fmt.Errorf("stuntgres: missing row source association")
					}
					if err := child(field, n.fields[slot]); err != nil {
						return err
					}
					slot++
				}
			}
			if slot != len(n.fields) {
				return fmt.Errorf("stuntgres: invalid row source association")
			}
			return nil
		}
	case parser.FieldExpr:
		if n, ok := bound.(fieldExpression); ok {
			b.captureViewComposite(path, compositeSelection, n.base, []int32{n.attribute})
			return child(r.Base, n.base)
		}
		if n, ok := bound.(functions.NullCount[BoundExpr, *EvalContext, ExprProperties]); ok && len(n.Args) == 1 {
			b.captureViewComposite(path, compositeDotBuiltin, n.Args[0], nil)
			captured := b.viewComposites[path]
			captured.function = n.Kind
			b.viewComposites[path] = captured
			return child(r.Base, n.Args[0])
		}
	case parser.ArrayExpr:
		if n, ok := bound.(arrayExpression); ok {
			return children(r.Elements, n.elements)
		}
	case parser.SubscriptExpr:
		if n, ok := bound.(subscriptExpression); ok && len(r.Subscripts) == len(n.subscripts) {
			if err := child(r.Base, n.base); err != nil {
				return err
			}
			for i, s := range r.Subscripts {
				if s.Lower != nil {
					if err := child(s.Lower, n.subscripts[i].lower); err != nil {
						return err
					}
				}
				if s.Upper != nil {
					if err := child(s.Upper, n.subscripts[i].upper); err != nil {
						return err
					}
				}
			}
			return nil
		}
	case parser.CollateExpr:
		if n, ok := bound.(collationExpression); ok {
			if b.viewCollations == nil {
				b.viewCollations = make(map[viewLiteralPath]uint32)
			}
			b.viewCollations[path] = n.usage.Collation.ID
			return child(r.Child, n.child)
		}
	case parser.CastExpr:
		// Domain casts retain top annotations outside their executable coercion.
		// Capture their original syntactic paths by mirroring them around the
		// coercion input, without changing the executable or frozen AST.
		var annotations BoundExpr
		if _, annotated := r.Child.(parser.CollateExpr); annotated && bound.ResultUsage().Type.IsDomain() {
			annotations = bound
			for {
				annotation, ok := bound.(collationExpression)
				if !ok {
					break
				}
				bound = annotation.child
			}
		}
		if b.viewCastUsages == nil {
			b.viewCastUsages = make(map[viewLiteralPath]sqltype.Usage)
		}
		b.viewCastUsages[path] = bound.ResultUsage()
		if coercion, ok := bound.(coercionExpression); ok && coercion.mode == sqltype.Explicit {
			bound = coercion.child
		}
		bound = restoreDomainCastCollations(annotations, bound)
		return child(r.Child, bound)
	case parser.RegclassExpr:
		if coercion, ok := bound.(coercionExpression); ok && coercion.mode == sqltype.Explicit {
			bound = coercion.child
		}
		return child(r.Child, bound)
	case parser.OperatorExpr:
		switch n := bound.(type) {
		case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
			return selectedArrayArguments(r.Args, n.Args)
		case rowComparisonExpression:
			for side, arg := range r.Args {
				row, ok := arg.(parser.RowExpr)
				if !ok || len(row.Fields) != len(n.comparisons) {
					return fmt.Errorf("stuntgres: mismatched row comparison syntax")
				}
				(*ordinal)++ // The represented RowExpr itself has no scalar datum.
				for i, field := range row.Fields {
					operand := n.comparisons[i].left
					if side == 1 {
						operand = n.comparisons[i].right
					}
					if err := child(field, operand); err != nil {
						return err
					}
				}
			}
			return nil
		case subqueryExpression:
			if len(r.Args) != 2 {
				return fmt.Errorf("stuntgres: mismatched row subquery syntax")
			}
			if err := b.captureSubqueryLeft(r.Args[0], n, path, ordinal); err != nil {
				return err
			}
			return child(r.Args[1], n)
		case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
			return children(r.Args, n.Args)
		case literalExpression:
			// Unary plus over a checked integer or numeric literal is identity.
			// Keep the captured value at the operand occurrence, matching the
			// syntax traversal used by validation and reconstruction.
			if len(r.Name.Parts) == 1 && r.Name.Parts[0].Text == "+" && len(r.Args) == 1 {
				if operand, ok := r.Args[0].(parser.LiteralExpr); ok && (operand.Value.Kind == parser.NumericValue || operand.Value.Kind == parser.IntegerValue) {
					return child(operand, n)
				}
			}
		case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
			return children(r.Args, n.Args[:])
		case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
			return children(r.Args, n.Args[:])
		case functions.String[BoundExpr, *EvalContext, ExprProperties]:
			return children(r.Args, n.Args)
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
		case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
			if err := child(r.Args[0], n.Left); err != nil {
				return err
			}
			return child(r.Args[1], n.Right)
		case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
			if err := child(r.Args[0], n.Left); err != nil {
				return err
			}
			return child(r.Args[1], n.Right)
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
		if n, ok := bound.(seriesExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(subscriptsExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(multirangeUnnestExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.JSONElements[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		var regexp *functions.RegexpCall[BoundExpr, *EvalContext, ExprProperties]
		switch n := bound.(type) {
		case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
			regexp = &n.RegexpCall
		case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
			regexp = &n.RegexpCall
		}
		if regexp != nil {
			written := make([]BoundExpr, len(regexp.Args))
			for i, slot := range regexp.WrittenSlots[:len(written)] {
				written[i] = regexp.Args[slot]
			}
			return children(r.Args, written)
		}
		if n, ok := bound.(installedLevenshteinFunctionExpression); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(installedCryptoFunctionExpression); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(installedFunctionExpression); ok {
			return children(r.Args, []BoundExpr{n.arg})
		}
		if n, ok := bound.(fieldExpression); ok && len(r.Args) == 1 {
			b.captureViewComposite(path, compositeSelection, n.base, []int32{n.attribute})
			return child(r.Args[0], n.base)
		}
		if _, ok := bound.(identityExpression); ok && len(r.Args) == 0 {
			return nil
		}
		if n, ok := bound.(functions.TransactionTime[*EvalContext, ExprProperties]); ok && len(r.Args) == 0 {
			return n.Validate(functionPropertiesMatch(n.Property, n.Kind.IntrinsicRule()))
		}
		if n, ok := bound.(functions.Random[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]); ok {
			// Retain the signature's implicit cast at the call occurrence:
			// PostgreSQL displays function argument coercions even though the
			// literal traversal below preserves the original operand type.
			if coercion, ok := n.Arg.(coercionExpression); ok && coercion.mode == sqltype.Implicit {
				if b.viewCastUsages == nil {
					b.viewCastUsages = make(map[viewLiteralPath]sqltype.Usage)
				}
				b.viewCastUsages[path] = coercion.usage
			}
			return children(r.Args, []BoundExpr{n.Arg})
		}
		if n, ok := bound.(advisoryExpression); ok {
			return children(r.Args, n.args)
		}
		if n, ok := bound.(functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]); ok {
			if err := child(r.Args[0], n.Pattern); err != nil {
				return err
			}
			return child(r.Args[1], n.Escape)
		}
		if n, ok := bound.(functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]); ok {
			if err := child(r.Args[0], n.Pattern); err != nil {
				return err
			}
			if !n.Omitted {
				return child(r.Args[1], n.Escape)
			}
			return nil
		}
		if n, ok := bound.(functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]); ok {
			if b.viewPosixSubstrings == nil {
				b.viewPosixSubstrings = make(map[viewLiteralPath]bool)
			}
			b.viewPosixSubstrings[path] = true
			return children(r.Args, n.Args[:])
		}
		if n, ok := bound.(functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]); ok {
			if b.viewSimilarSubstrings == nil {
				b.viewSimilarSubstrings = make(map[viewLiteralPath]bool)
			}
			b.viewSimilarSubstrings[path] = true
			return children(r.Args, n.Args[:])
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
		if n, ok := bound.(accessInquiryExpression); ok {
			return children(r.Args, n.arguments())
		}
		if n, ok := bound.(namespaceInquiryExpression); ok {
			if n.arg != nil && len(r.Args) == 1 {
				return child(r.Args[0], n.arg)
			}
			return nil
		}
		if n, ok := bound.(literalExpression); ok && n.value.IsNull() && n.value.Type().ID() == sqltype.Bool {
			switch r.Name.Parts[len(r.Name.Parts)-1].Text {
			case "pg_indexam_has_property", "pg_index_has_property", "pg_index_column_has_property":
				node, err := b.bindIndexPropertyStructure(r, false)
				if err != nil {
					return err
				}
				return children(r.Args, node.(indexPropertyExpression).children())
			case "pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible", "pg_collation_actual_version":
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
		if n, ok := bound.(functions.Binary[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Arguments())
		}
		if n, ok := bound.(functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Arguments())
		}
		if n, ok := bound.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Arguments())
		}
		if n, ok := bound.(functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Arguments())
		}
		if n, ok := bound.(functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Arguments())
		}
		if n, ok := bound.(functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Arguments())
		}
		if n, ok := bound.(functions.Timezone[BoundExpr, *EvalContext, ExprProperties]); ok && n.Function {
			if n.Local {
				return children(r.Args, []BoundExpr{n.Value})
			}
			return children(r.Args, []BoundExpr{n.Zone, n.Value})
		}
		if n, ok := bound.(functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Arguments())
		}
		if n, ok := bound.(functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]); ok {
			if len(r.Args) != n.WrittenCount {
				return fmt.Errorf("stuntgres: mismatched JSON stripping syntax")
			}
			for i, arg := range r.Args {
				if err := child(arg, n.Args[n.WrittenSlots[i]]); err != nil {
					return err
				}
			}
			return nil
		}
		if n, ok := bound.(functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]); ok {
			if len(r.Args) != n.WrittenCount {
				return fmt.Errorf("stuntgres: mismatched JSONB replacement syntax")
			}
			for i, arg := range r.Args {
				if err := child(arg, n.Args[n.WrittenSlots[i]]); err != nil {
					return err
				}
			}
			return nil
		}
		if n, ok := bound.(functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]); ok {
			if len(r.Args) != n.WrittenCount {
				return fmt.Errorf("stuntgres: mismatched constructor syntax")
			}
			for i, arg := range r.Args {
				if err := child(arg, n.Args[n.WrittenSlots[i]]); err != nil {
					return err
				}
			}
			return nil
		}
		if n, ok := bound.(functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args[:])
		}
		if n, ok := bound.(functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args[:])
		}
		if n, ok := bound.(functions.String[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]); ok {
			return selectedArrayArguments(r.Args, n.Args)
		}
		if n, ok := bound.(functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]); ok {
			return selectedArrayArguments(r.Args, n.Args)
		}
		if n, ok := bound.(functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]); ok && len(r.Args) == 1 {
			return child(r.Args[0], n.Arg)
		}
		if n, ok := bound.(functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.Enum[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.Math[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(currentSchemasExpression); ok && len(r.Args) == 1 {
			return child(r.Args[0], n.arg)
		}
		if n, ok := bound.(functions.NullCount[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(functions.Format[BoundExpr, *EvalContext, ExprProperties]); ok {
			return children(r.Args, n.Args)
		}
	case parser.CallExpr:
		if n, ok := bound.(sequenceCallExpression); ok {
			return children(r.Args, n.args)
		}
	}
	return fmt.Errorf("stuntgres: mismatched view literal expression %T / %T", raw, bound)
}

func (v *viewDefinition) validateLiterals(catalog CatalogRead) error {
	count, columnCount, starCount, compositeCount := 0, 0, 0, 0
	for frame, project := range v.frames {
		s := project.syntax
		if s == nil {
			if frame != 1 || v.expressionSyntax == nil {
				return fmt.Errorf("stuntgres: missing stored query syntax")
			}
			ordinal := 0
			if err := v.validateLiteralTree(catalog, v.expressionSyntax, viewLiteralPath{query: frame, clause: viewTargetLiteral}, &ordinal, &count, &columnCount, &compositeCount); err != nil {
				return err
			}
			continue
		}
		if s.Set != nil {
			set := querySetNode(viewFromRoot(project))
			if set == nil || set.left == nil || set.right == nil || set.left.syntax != s.Set.Left || set.right.syntax != s.Set.Right || set.operation != s.Set.Operator || set.all != s.Set.All {
				return fmt.Errorf("stuntgres: invalid frozen set syntax frames")
			}
			left, right := v.queries[s.Set.Left], v.queries[s.Set.Right]
			if left == nil || right == nil || left.frame != set.left.frame || right.frame != set.right.frame {
				return fmt.Errorf("stuntgres: invalid frozen set syntax frames")
			}
		}
		validate := func(raw parser.Expr, clause viewLiteralClause, target int) error {
			ordinal := 0
			return v.validateLiteralTree(catalog, raw, viewLiteralPath{query: frame, clause: clause, target: target}, &ordinal, &count, &columnCount, &compositeCount)
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
		if s.Distinct != nil {
			for i, raw := range s.Distinct.On {
				if err := validate(raw, viewDistinctOnLiteral, i); err != nil {
					return err
				}
			}
		}
		if err := validate(s.Where, viewWhereLiteral, 0); err != nil {
			return err
		}
		i := 0
		if err := s.GroupBy.VisitExpressions(func(raw parser.Expr) error {
			err := validate(raw, viewGroupLiteral, i)
			i++
			return err
		}); err != nil {
			return err
		}
		if err := validate(s.Having, viewHavingLiteral, 0); err != nil {
			return err
		}
		for i, definition := range s.Windows {
			ordinal := 0
			path := viewLiteralPath{query: frame, clause: viewWindowLiteral, target: i}
			for _, key := range definition.Spec.Partition {
				if err := v.validateLiteralTree(catalog, key, path, &ordinal, &count, &columnCount, &compositeCount); err != nil {
					return err
				}
			}
			for _, key := range definition.Spec.Order {
				if err := v.validateLiteralTree(catalog, key.Expr, path, &ordinal, &count, &columnCount, &compositeCount); err != nil {
					return err
				}
			}
			for _, offset := range definition.Spec.Offsets() {
				if err := v.validateLiteralTree(catalog, offset, path, &ordinal, &count, &columnCount, &compositeCount); err != nil {
					return err
				}
			}
		}
		// VALUES owns its decorations' source-ordinal literal paths even when
		// the SELECT syntax wrapper also exposes those same tail pointers.
		if s.Values == nil && s.Order != nil {
			for i, key := range s.Order.Keys {
				if err := validate(key.Expr, viewOrderLiteral, i); err != nil {
					return err
				}
			}
		}
		if s.Values == nil && s.Bounds != nil {
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
			case *parser.FunctionTable:
				table, ok := viewFromRoot(node).(*functionFromNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored string function frame")
				}
				for i, call := range raw.Calls {
					if table.regexpNamespace {
						path := viewLiteralPath{query: frame, clause: viewFunctionLiteral, source: table.viewOrdinal, target: i}
						ordinal := 0
						if err := v.validateLiteralTree(catalog, call, path, &ordinal, &count, &columnCount, &compositeCount); err != nil {
							return err
						}
					} else if err := validate(call, viewFunctionLiteral, table.viewOrdinal+i); err != nil {
						return err
					}
				}
				return nil
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
				return v.validateValuesLiterals(catalog, raw, frame, values.viewOrdinal, &count, &columnCount, &compositeCount)
			case *parser.FunctionFrom:
				function, ok := viewFromRoot(node).(*functionFromNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored function frame")
				}
				for i, call := range raw.Calls {
					path := viewLiteralPath{query: frame, clause: viewFunctionLiteral, source: function.viewOrdinal, target: i}
					ordinal := 0
					if err := v.validateLiteralTree(catalog, call, path, &ordinal, &count, &columnCount, &compositeCount); err != nil {
						return err
					}
				}
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
	if count != len(v.literals) || columnCount != len(v.columns) || starCount != len(v.stars) || compositeCount != len(v.composites) {
		return fmt.Errorf("stuntgres: invalid view literal occurrence path")
	}
	return nil
}

// Scalar stars and ordinary column syntax close over the same captured frame
// and slot, independently of composite expansion occurrences.
func (v *viewDefinition) validateColumnOccurrence(path viewLiteralPath) error {
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
	return nil
}

func (v *viewDefinition) validateLiteralTree(catalog CatalogRead, raw parser.Expr, path viewLiteralPath, ordinal, count, columnCount, compositeCount *int) error {
	if raw == nil {
		return nil
	}
	path.node = *ordinal
	(*ordinal)++
	child := func(raw parser.Expr) error {
		return v.validateLiteralTree(catalog, raw, path, ordinal, count, columnCount, compositeCount)
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
	case parser.GroupingExpr:
		return args(r.Args)
	case parser.InListExpr:
		if err := child(r.Left); err != nil {
			return err
		}
		return args(r.Items)
	case parser.ArrayComparisonExpr:
		if err := child(r.Left); err != nil {
			return err
		}
		return child(r.Right)
	case parser.SQLJSONExpr:
		if r.Query != nil {
			if v.queries[r.Query] == nil {
				return fmt.Errorf("stuntgres: missing frozen SQL/JSON query syntax")
			}
			return nil
		}
		return args(r.Args)
	case parser.JSONPredicateExpr:
		return child(r.Child)
	case parser.SubqueryExpr:
		if v.queries[r.Query] == nil {
			return fmt.Errorf("stuntgres: missing frozen subquery syntax")
		}
		return child(r.Left)
	case parser.FieldExpr:
		captured, ok := v.composites[path]
		if !ok || r.Expansion && captured.kind != compositeExpansion || !r.Expansion && captured.kind != compositeSelection && captured.kind != compositeDotBuiltin {
			return fmt.Errorf("stuntgres: missing frozen field interpretation")
		}
		if captured.kind == compositeDotBuiltin && captured.function != functions.NullCountFunctionKind(r.Field.Text) {
			return fmt.Errorf("stuntgres: invalid frozen dot provider")
		}
		if err := captured.validate(catalog); err != nil {
			return err
		}
		if err := v.validateCompositeSource(captured); err != nil {
			return err
		}
		(*compositeCount)++
		return child(r.Base)
	case parser.RowExpr:
		return args(r.Fields)
	case parser.IdentityExpr:
		return nil
	case parser.CurrentDateTimeExpr:
		return nil
	case parser.LiteralExpr, parser.TypedLiteralExpr:
		if literal, ok := v.literals[path]; ok {
			if err := validateRegroleStoredLiteral(literal.value); err != nil {
				return err
			}
			usage := literal.ResultUsage()
			if !usage.Valid() || literal.value.Type() != usage.Type {
				return fmt.Errorf("stuntgres: invalid view literal usage")
			}
			if err := validateUsageCollation(catalog, usage); err != nil {
				return err
			}
			typ, ok := catalog.LookupType(usage.Type.ID())
			if !ok || typ != usage.Type {
				return fmt.Errorf("stuntgres: foreign view literal type")
			}
			if ref := literal.regclassRef; ref != (ObjectRef{}) {
				consumer, _ := catalog.state.descriptors[v.consumer].(*relationDescriptor)
				if !storedViewReferenceValid(catalog, consumer, ref) || ref.Address.ClassID != RelationClassID || ref.Address.SubID != 0 || usage.Type.ID() != sqltype.Regclass || literal.value.IsNull() {
					return fmt.Errorf("stuntgres: invalid view regclass literal authority")
				}
			}
			(*count)++
			return nil
		}
		if integer, ok := raw.(parser.LiteralExpr); ok && path.clause == viewValuesOrderLiteral && path.node == 0 && integer.Value.Kind == parser.IntegerValue {
			root := viewValuesAt(v.frames[path.query], path.source)
			if root == nil {
				return fmt.Errorf("stuntgres: missing VALUES order frame")
			}
			_, _, err := orderOrdinal(raw, len(root.Schema()), "ORDER BY")
			return err
		}
		if integer, ok := raw.(parser.LiteralExpr); ok && (path.clause == viewGroupLiteral || path.clause == viewOrderLiteral || path.clause == viewDistinctOnLiteral) && path.node == 0 && integer.Value.Kind == parser.IntegerValue {
			if path.clause != viewGroupLiteral {
				clause := "ORDER BY"
				if path.clause == viewDistinctOnLiteral {
					clause = "DISTINCT ON"
				}
				_, _, err := orderOrdinal(raw, len(v.frames[path.query].Schema()), clause)
				return err
			}
			_, _, err := groupOrdinal(raw, len(v.frames[path.query].Schema()))
			return err
		}
		return fmt.Errorf("stuntgres: missing view literal occurrence")
	case parser.ColumnExpr:
		if captured, whole := v.composites[path]; whole {
			if captured.kind != compositeWholeRow {
				return fmt.Errorf("stuntgres: invalid frozen scalar whole row")
			}
			if err := captured.validate(catalog); err != nil {
				return err
			}
			if err := v.validateCompositeSource(captured); err != nil {
				return err
			}
			if v.namespaces[captured.source.namespace-1].qualifiers[captured.source.qualifier].name != r.Name.Parts[len(r.Name.Parts)-1].Text {
				return fmt.Errorf("stuntgres: mismatched frozen whole row qualifier")
			}
			(*compositeCount)++
			return nil
		}
		if err := v.validateColumnOccurrence(path); err != nil {
			return err
		}
		(*columnCount)++
		return nil
	case parser.StarExpr:
		if _, scalar := v.columns[path]; scalar {
			if err := v.validateColumnOccurrence(path); err != nil {
				return err
			}
			(*columnCount)++
			return nil
		}
		if _, relationStar := v.stars[path]; !relationStar {
			captured, ok := v.composites[path]
			if !ok || captured.kind != compositeExpansion && captured.kind != compositeWholeRow {
				return fmt.Errorf("stuntgres: missing frozen whole row interpretation")
			}
			if err := captured.validate(catalog); err != nil {
				return err
			}
			if err := v.validateCompositeSource(captured); err != nil {
				return err
			}
			if captured.source.namespace == 0 || v.namespaces[captured.source.namespace-1].qualifiers[captured.source.qualifier].name != r.Qualifier.Parts[len(r.Qualifier.Parts)-1].Text {
				return fmt.Errorf("stuntgres: mismatched frozen whole row star")
			}
			(*compositeCount)++
		}
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
		if r.Window != nil {
			for _, key := range r.Window.Partition {
				if err := child(key); err != nil {
					return err
				}
			}
			for _, key := range r.Window.Order {
				if err := child(key.Expr); err != nil {
					return err
				}
			}
			for _, offset := range r.Window.Offsets() {
				if err := child(offset); err != nil {
					return err
				}
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
		id, ok := v.collations[path]
		if !ok {
			return fmt.Errorf("stuntgres: missing frozen view collation")
		}
		d, ok := catalog.state.descriptors[ObjectAddress{ClassID: CollationClassID, ObjectID: OID(id)}].(*collationDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing view collation identity")
		}
		if _, err := d.usage(); err != nil {
			return fmt.Errorf("stuntgres: invalid view collation provider")
		}
		return child(r.Child)
	case parser.CastExpr:
		return child(r.Child)
	case parser.RegclassExpr:
		return child(r.Child)
	case parser.ArrayExpr:
		return args(r.Elements)
	case parser.SubscriptExpr:
		var storage [13]parser.Expr
		return args(r.Children(&storage))
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
		if captured, projected := v.composites[path]; projected {
			if captured.kind != compositeSelection || len(r.Args) != 1 {
				return fmt.Errorf("stuntgres: invalid frozen functional projection")
			}
			if err := captured.validate(catalog); err != nil {
				return err
			}
			if err := v.validateCompositeSource(captured); err != nil {
				return err
			}
			(*compositeCount)++
		}
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
