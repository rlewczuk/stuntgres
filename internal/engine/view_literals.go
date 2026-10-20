package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) captureViewLiterals(raw parser.Expr, bound expr.Expr, clause viewLiteralClause, target int) error {
	path := viewLiteralPath{Query: b.frameID(), Clause: clause, Target: target}
	ordinal := 0
	return b.captureViewLiteralTree(raw, bound, path, &ordinal)
}

func (b *expressionBinder) captureViewLiteralTree(raw parser.Expr, bound expr.Expr, path viewLiteralPath, ordinal *int) error {
	return b.captureNormalizedViewLiteralTree(b.normalizeStoredExpression(raw), bound, path, ordinal)
}

func (b *expressionBinder) captureNormalizedViewLiteralTree(raw parser.Expr, bound expr.Expr, path viewLiteralPath, ordinal *int) error {
	if raw == nil {
		return nil
	}
	path.Node = *ordinal
	(*ordinal)++
	// Contextual ARRAY/ROW member casts have an executable plan but no raw
	// member CastExpr. Their enclosing constructor owns the annotation.
	for {
		coercion, ok := bound.(plan.CoercionExpr)
		if !ok || coercion.Mode == sqltype.Explicit && !coercion.SyntheticConstructorElement {
			break
		}
		if constructor, ok := raw.(parser.SQLJSONExpr); ok && constructor.Returning != nil && coercion.Mode == sqltype.Assignment {
			break
		}
		if aggregate, ok := raw.(parser.AggregateExpr); ok && aggregate.JSON != nil && aggregate.JSON.Returning != nil && coercion.Mode == sqltype.Assignment {
			break // RETURNING owns this assignment conversion in the raw syntax.
		}
		bound = coercion.Child
	}
	child := func(raw parser.Expr, bound expr.Expr) error {
		childPath := path
		childPath.Argument = 0
		return b.captureNormalizedViewLiteralTree(raw, bound, childPath, ordinal)
	}
	children := func(raw []parser.Expr, bound []expr.Expr) error {
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
	protocol := func(kind expr.CaptureKind, raw []parser.Expr) error {
		hook, ok := bound.(expr.Capturable)
		if !ok {
			return fmt.Errorf("stuntgres: mismatched view literal expression %T / %T", raw, bound)
		}
		operands, admitted, err := hook.Capture(kind)
		if err != nil {
			return err
		}
		if !admitted {
			return fmt.Errorf("stuntgres: mismatched view literal capture kind %d / %T", kind, bound)
		}
		if err := operands.Check(hook, len(raw)); err != nil {
			return err
		}
		if operands.PosixSubstring {
			if b.capture.PosixSubstrings == nil {
				b.capture.PosixSubstrings = make(map[viewLiteralPath]bool)
			}
			b.capture.PosixSubstrings[path] = true
		}
		if operands.SimilarSubstring {
			if b.capture.SimilarSubstrings == nil {
				b.capture.SimilarSubstrings = make(map[viewLiteralPath]bool)
			}
			b.capture.SimilarSubstrings[path] = true
		}
		if operands.DotBuiltin != "" {
			base, _ := operands.At(hook, 0)
			b.capture.CaptureViewComposite(path, analyze.CompositeDotBuiltin, base, nil)
			captured := b.capture.Composites[path]
			captured.Function = functions.NullCountFunctionKind(operands.DotBuiltin)
			b.capture.Composites[path] = captured
		}
		for i, written := range raw {
			arg, _ := operands.At(hook, i) // Check validated every slot before any map write.
			if operands.SelectedImplicit || operands.CallImplicit {
				if coercion, ok := arg.(plan.CoercionExpr); ok && coercion.Mode == sqltype.Implicit {
					if b.capture.CastUsages == nil {
						b.capture.CastUsages = make(map[viewLiteralPath]sqltype.Usage)
					}
					key := path
					if operands.SelectedImplicit {
						key.Argument = i + 1
					}
					b.capture.CastUsages[key] = coercion.Usage
				}
			}
			if operands.JSONSources {
				arg = sqlJSONSource(arg)
			}
			if err := child(written, arg); err != nil {
				return err
			}
		}
		return nil
	}
	switch r := raw.(type) {
	case parser.ParameterExpr:
		// MV parameter admission follows complete typed analysis. No such
		// definition can publish; leave this occurrence to the later 0A000.
		if parameter, ok := bound.(plan.ParameterExpr); b.consumer.materializedDefinition && ok && parameter.Number == r.Number {
			return nil
		}
	case parser.ArrayComparisonExpr:
		if n, ok := bound.(plan.ScalarArrayComparisonExpr); ok {
			if err := child(r.Left, n.Left); err != nil {
				return err
			}
			if n.Array == nil {
				// A homogeneous IN batch has synthetic frozen ARRAY syntax,
				// but no array construction in its executable representation.
				return child(r.Right, plan.ArrayExpr{Elements: n.Items})
			}
			return child(r.Right, n.Array)
		}
	case parser.SQLJSONExpr:
		if r.Returning != nil {
			if b.capture.CastUsages == nil {
				b.capture.CastUsages = make(map[viewLiteralPath]sqltype.Usage)
			}
			b.capture.CastUsages[path] = bound.ResultUsage()
			if c, ok := bound.(plan.CoercionExpr); ok {
				bound = c.Child
			}
		}
		if r.Query != nil {
			if n, ok := bound.(plan.SubqueryExpr); ok {
				return b.captureSubqueryLeft(nil, n, path, ordinal)
			}
		} else {
			return protocol(expr.CaptureSQLJSON, r.Args)
		}
	case parser.JSONPredicateExpr:
		return protocol(expr.CaptureJSONPredicate, []parser.Expr{r.Child})
	case parser.SubqueryExpr:
		if n, ok := bound.(plan.SubqueryExpr); ok {
			return b.captureSubqueryLeft(r.Left, n, path, ordinal)
		}
	case parser.IdentityExpr:
		return protocol(expr.CaptureIdentity, nil)
	case parser.CurrentDateTimeExpr:
		return protocol(expr.CaptureCurrentDateTime, nil)
	case parser.LiteralExpr, parser.TypedLiteralExpr:
		if _, typed := r.(parser.TypedLiteralExpr); typed {
			if coercion, ok := bound.(plan.CoercionExpr); ok && coercion.Mode == sqltype.Explicit {
				if literal, ok := coercion.Child.(plan.LiteralExpr); ok && literal.Usage.Type == coercion.Usage.Type {
					// The stored query retains the executable typmod coercion.
					// Literal replay writes its raw value with the typed occurrence's
					// annotation, so ordinary binding reconstructs that same coercion.
					literal.Usage = coercion.Usage
					bound = literal
				}
			}
		}
		if _, plain := r.(parser.LiteralExpr); plain {
			// Explicit constructor-member coercions have no raw CastExpr.
			// Capture the literal input, retaining the executable domain/typmod
			// conversion on the stored tree and reconstructing it on replay.
			for {
				coercion, ok := bound.(plan.CoercionExpr)
				if !ok {
					break
				}
				bound = coercion.Child
			}
		}
		if literal, ok := bound.(plan.LiteralExpr); ok {
			if err := validateRegroleStoredLiteral(literal.Value); err != nil {
				return err
			}
			if b.capture.Literals == nil {
				b.capture.Literals = make(map[viewLiteralPath]plan.LiteralExpr)
			}
			b.capture.Literals[path] = literal
			return nil
		}
	case parser.ColumnExpr, parser.StarExpr:
		if column, ok := bound.(plan.ColumnExpr); ok {
			if b.capture.Columns == nil {
				b.capture.Columns = make(map[viewLiteralPath]viewColumn)
			}
			captured := viewColumn{Frame: column.Frame, Slot: column.Slot}
			if column.Scope == rulePseudoScope {
				captured.RuleRef = column.Origin
				captured.RuleSide = column.Frame
			}
			b.capture.Columns[path] = captured
			return nil
		}
		if row, ok := bound.(plan.RowExpr); ok && row.WholeNames != nil {
			b.capture.CaptureViewComposite(path, analyze.CompositeWholeRow, row, nil)
			return nil
		}
	case parser.GroupingExpr:
		if n, ok := bound.(plan.GroupingExpr); ok {
			return children(r.Args, n.Args)
		}
	case parser.AggregateExpr:
		if r.JSON != nil && r.JSON.Returning != nil {
			if b.capture.CastUsages == nil {
				b.capture.CastUsages = make(map[viewLiteralPath]sqltype.Usage)
			}
			b.capture.CastUsages[path] = bound.ResultUsage()
			if conversion, ok := bound.(plan.CoercionExpr); ok {
				bound = conversion.Child
			}
		}
		if n, ok := bound.(*plan.WindowExpr); ok && r.Window != nil {
			if n.Call < 0 || n.Call >= len(b.grouping.windowCalls) {
				return fmt.Errorf("stuntgres: invalid stored window occurrence")
			}
			if call := b.grouping.windowCalls[n.Call]; call.Argument != nil {
				if err := child(r.Args[0], call.Argument); err != nil {
					return err
				}
			}
			if call := b.grouping.windowCalls[n.Call]; call.Ordinal != nil {
				if err := child(r.Args[1], call.Ordinal); err != nil {
					return err
				}
			}
			if call := b.grouping.windowCalls[n.Call]; call.DefaultValue != nil {
				if err := child(r.Args[2], call.DefaultValue); err != nil {
					return err
				}
			}
			spec := b.grouping.windowCalls[n.Call].Spec
			for i, key := range r.Window.Partition {
				if err := child(key, spec.Partition[i]); err != nil {
					return err
				}
			}
			for i, key := range r.Window.Order {
				if err := child(key.Expr, spec.Order[spec.OrderMap[i]].Expression); err != nil {
					return err
				}
			}
			if r.Window.Frame != nil {
				if err := child(r.Window.Frame.Start.Offset, spec.Frame.Start.Offset); err != nil {
					return err
				}
				if err := child(r.Window.Frame.End.Offset, spec.Frame.End.Offset); err != nil {
					return err
				}
			}
			return nil
		}
		if n, ok := bound.(plan.AggregateExpr); ok {
			if call, ok := b.aggregateCallAt(n); ok {
				if call.Procedure != nil {
					if err := b.captureIntaggReference(path, call.Procedure, call.Ref, "int_array_aggregate"); err != nil {
						return err
					}
				}
				for i, arg := range r.Args {
					expression := call.Argument
					if r.Within {
						if i >= len(call.Direct) {
							return fmt.Errorf("stuntgres: invalid stored direct arguments")
						}
						expression = call.Direct[i]
					} else if i == 1 {
						expression = call.Separator
					} else if i > 1 {
						return fmt.Errorf("stuntgres: invalid stored aggregate arguments")
					}
					if call.Operation.Hypothetical() {
						expression = expression.(plan.CollationExpr).Child
					}
					if conversion, ok := expression.(plan.CoercionExpr); ok && conversion.Plan.IsJSONFormattedInput() {
						expression = conversion.Child
					}
					if expression == nil {
						return fmt.Errorf("stuntgres: invalid stored aggregate arguments")
					}
					if err := child(arg, expression); err != nil {
						return err
					}
				}
				if len(r.Order) != len(call.Order) {
					return fmt.Errorf("stuntgres: invalid stored aggregate ordering")
				}
				for i, key := range r.Order {
					expression := call.Order[i].Expression
					if call.Operation.Hypothetical() {
						expression = expression.(plan.CollationExpr).Child
					}
					if err := child(key.Expr, expression); err != nil {
						return err
					}
				}
				if r.Filter != nil {
					if err := child(r.Filter, call.Filter); err != nil {
						return err
					}
				}
				return nil
			}
		}
	case parser.TimeZoneExpr:
		if r.Local {
			return protocol(expr.CaptureLocal, []parser.Expr{r.Value})
		}
		return protocol(expr.CaptureTimeZone, []parser.Expr{r.Value, r.Zone})
	case parser.RowExpr:
		if n, ok := bound.(plan.RowExpr); ok {
			slot := 0
			for target, field := range r.Fields {
				if expansion, expanded := n.Expansions[target]; expanded {
					if err := b.captureCompositeExpansion(field, expansion, path, ordinal); err != nil {
						return err
					}
					slot += len(expansion.Fields)
				} else {
					if slot >= len(n.Fields) {
						return fmt.Errorf("stuntgres: missing row source association")
					}
					if err := child(field, n.Fields[slot]); err != nil {
						return err
					}
					slot++
				}
			}
			if slot != len(n.Fields) {
				return fmt.Errorf("stuntgres: invalid row source association")
			}
			return nil
		}
	case parser.FieldExpr:
		if n, ok := bound.(plan.FieldExpr); ok {
			b.capture.CaptureViewComposite(path, analyze.CompositeSelection, n.Base, []int32{n.Attribute})
			return child(r.Base, n.Base)
		}
		return protocol(expr.CaptureField, []parser.Expr{r.Base})
	case parser.ArrayExpr:
		if n, ok := bound.(plan.ArrayExpr); ok {
			return children(r.Elements, n.Elements)
		}
	case parser.SubscriptExpr:
		if n, ok := bound.(plan.SubscriptExpr); ok && len(r.Subscripts) == len(n.Subscripts) {
			if err := child(r.Base, n.Base); err != nil {
				return err
			}
			for i, s := range r.Subscripts {
				if s.Lower != nil {
					if err := child(s.Lower, n.Subscripts[i].Lower); err != nil {
						return err
					}
				}
				if s.Upper != nil {
					if err := child(s.Upper, n.Subscripts[i].Upper); err != nil {
						return err
					}
				}
			}
			return nil
		}
	case parser.CollateExpr:
		if n, ok := bound.(plan.CollationExpr); ok {
			if b.capture.Collations == nil {
				b.capture.Collations = make(map[viewLiteralPath]uint32)
			}
			b.capture.Collations[path] = n.Usage.Collation.ID
			return child(r.Child, n.Child)
		}
	case parser.CastExpr:
		// Domain casts retain top annotations outside their executable coercion.
		// Capture their original syntactic paths by mirroring them around the
		// coercion input, without changing the executable or frozen AST.
		var annotations expr.Expr
		if _, annotated := r.Child.(parser.CollateExpr); annotated && bound.ResultUsage().Type.IsDomain() {
			annotations = bound
			for {
				annotation, ok := bound.(plan.CollationExpr)
				if !ok {
					break
				}
				bound = annotation.Child
			}
		}
		if b.capture.CastUsages == nil {
			b.capture.CastUsages = make(map[viewLiteralPath]sqltype.Usage)
		}
		b.capture.CastUsages[path] = bound.ResultUsage()
		if coercion, ok := bound.(plan.CoercionExpr); ok && coercion.Mode == sqltype.Explicit {
			bound = coercion.Child
		}
		bound = analyze.RestoreDomainCastCollations(annotations, bound)
		return child(r.Child, bound)
	case parser.RegclassExpr:
		if coercion, ok := bound.(plan.CoercionExpr); ok && coercion.Mode == sqltype.Explicit {
			bound = coercion.Child
		}
		return child(r.Child, bound)
	case parser.OperatorExpr:
		switch n := bound.(type) {
		case plan.RowComparisonExpr:
			return b.captureRowComparison(r.Args, n, path, ordinal)
		case plan.SubqueryExpr:
			if len(r.Args) != 2 {
				return fmt.Errorf("stuntgres: mismatched row subquery syntax")
			}
			if err := b.captureSubqueryLeft(r.Args[0], n, path, ordinal); err != nil {
				return err
			}
			return child(r.Args[1], n)
		case plan.LiteralExpr:
			// Unary plus over a checked integer or numeric literal is identity.
			// Keep the captured value at the operand occurrence, matching the
			// syntax traversal used by validation and reconstruction.
			if len(r.Name.Parts) == 1 && r.Name.Parts[0].Text == "+" && len(r.Args) == 1 {
				if operand, ok := r.Args[0].(parser.LiteralExpr); ok && (operand.Value.Kind == parser.NumericValue || operand.Value.Kind == parser.IntegerValue) {
					return child(operand, n)
				}
			}
		case plan.BitwiseExpr:
			if err := child(r.Args[0], n.Left); err != nil {
				return err
			}
			if len(r.Args) == 2 {
				return child(r.Args[1], n.Right)
			}
			return nil
		case plan.SelectedOperatorExpr:
			if b.capture.OperatorRefs == nil {
				b.capture.OperatorRefs = make(map[viewLiteralPath]ids.ObjectRef)
			}
			b.capture.OperatorRefs[path] = n.Ref
			var operands []expr.Expr
			plan.ExpressionChildren(n.Provider, func(arg expr.Expr) {
				if arg != nil {
					operands = append(operands, arg)
				}
			})
			return children(r.Args, operands)
		case plan.ArithmeticExpr:
			if err := child(r.Args[0], n.Left); err != nil {
				return err
			}
			if len(r.Args) == 2 {
				return child(r.Args[1], n.Right)
			}
			return nil
		case plan.LikeExpr:
			if err := child(r.Args[0], n.Left); err != nil {
				return err
			}
			return child(r.Args[1], n.Right)
		case plan.ComparisonExpr:
			if err := child(r.Args[0], n.Left); err != nil {
				return err
			}
			return child(r.Args[1], n.Right)
		}
		return protocol(expr.CaptureOperator, r.Args)
	case parser.LogicalExpr:
		if n, ok := bound.(plan.LogicalExpr); ok {
			if err := child(r.Left, n.Left); err != nil {
				return err
			}
			return child(r.Right, n.Right)
		}
	case parser.PredicateExpr:
		switch n := bound.(type) {
		case plan.RowComparisonExpr:
			return b.captureRowComparison([]parser.Expr{r.Left, r.Right}, n, path, ordinal)
		case plan.NullTestExpr:
			if (r.Kind == parser.IsDistinct || r.Kind == parser.IsNotDistinct) && rawNull(r.Left) {
				return child(r.Right, n.Child)
			}
			return child(r.Left, n.Child)
		case plan.TruthTestExpr:
			return child(r.Left, n.Child)
		case plan.DistinctExpr:
			if err := child(r.Left, n.Left); err != nil {
				return err
			}
			return child(r.Right, n.Right)
		}
	case parser.ConditionalExpr:
		if n, ok := bound.(plan.ConditionalExpr); ok {
			return children(r.Args, n.Args)
		}
	case parser.CaseExpr:
		if n, ok := bound.(plan.CaseExpr); ok && len(r.Arms) == len(n.Arms) {
			if err := child(r.Test, n.Test); err != nil {
				return err
			}
			for i, arm := range r.Arms {
				if err := child(arm.Condition, n.Arms[i].Condition); err != nil {
					return err
				}
				if err := child(arm.Result, n.Arms[i].Result); err != nil {
					return err
				}
			}
			return child(r.Else, n.Otherwise)
		}
	case parser.FunctionExpr:
		if n, ok := bound.(plan.SQLRoutineExpr); ok {
			if !r.StoredSQLRoutine {
				return fmt.Errorf("stuntgres: missing normalized SQL routine syntax")
			}
			if err := b.captureSQLRoutineReference(path, n); err != nil {
				return err
			}
			return protocol(expr.CaptureFunction, r.Args)
		}
		if n, ok := bound.(plan.SeriesExpr); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.SubscriptsExpr); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.UnnestExpr); ok {
			if n.Procedure != nil {
				if err := b.captureIntaggReference(path, n.Procedure, n.Ref, "int_array_enum"); err != nil {
					return err
				}
			}
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.InstalledSoundexExpr); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.InstalledMetaphoneExpr); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.InstalledLevenshteinExpr); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.InstalledUUIDOsspExpr); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.InstalledCryptoExpr); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.InstalledTextExpr); ok {
			return children(r.Args, []expr.Expr{n.Arg})
		}
		if n, ok := bound.(plan.FieldExpr); ok && len(r.Args) == 1 {
			b.capture.CaptureViewComposite(path, analyze.CompositeSelection, n.Base, []int32{n.Attribute})
			return child(r.Args[0], n.Base)
		}
		if n, ok := bound.(plan.LiteralExpr); ok && n.Value.IsNull() {
			switch r.Name.Parts[len(r.Name.Parts)-1].Text {
			case "pg_indexam_has_property", "pg_index_has_property", "pg_index_column_has_property":
				node, err := functions.BindIndexPropertyStructure(b, r, false)
				if err != nil {
					return err
				}
				return children(r.Args, node.(expr.Composite).Children())
			case "pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible", "pg_collation_actual_version", "pg_database_collation_actual_version", "pg_import_system_collations":
				structure, err := functions.BindVisibilityInquiryStructure(b, r)
				if err != nil {
					return err
				}
				return children(r.Args, structure.Children())
			}
		}
		if n, ok := bound.(plan.LiteralExpr); ok && n.Value.IsNull() && n.Value.Type().ID() == sqltype.Text {
			switch r.Name.Parts[len(r.Name.Parts)-1].Text {
			case "col_description", "obj_description", "shobj_description":
				visible, err := b.builtinVisible(r.Name, analyze.FunctionLookup)
				if err != nil {
					return err
				}
				// Reconstruct only for typed literal/column capture. Keep the
				// executable NULL fold, with no child evaluation or preflight.
				structure, err := functions.BindCommentInquiryStructure(b, r, visible)
				if err != nil {
					return err
				}
				return children(r.Args, structure.Children())
			}
		}
		if n, ok := bound.(plan.ExtensionPathsExpr); ok && len(r.Args) == 1 {
			return child(r.Args[0], n.Arg)
		}
		return protocol(expr.CaptureFunction, r.Args)
	case parser.CallExpr:
		// A strict NULL fold leaves no capturable call: keep the mismatch.
		if _, ok := bound.(expr.Capturable); ok {
			return protocol(expr.CaptureFunction, r.Args)
		}
	}
	return fmt.Errorf("stuntgres: mismatched view literal expression %T / %T", raw, bound)
}

func (v *viewDefinition) validateLiterals(catalog catalog.Read) error {
	count, columnCount, starCount, compositeCount, routineCount := 0, 0, 0, 0, 0
	for frame, project := range v.frames {
		s := project.Syntax
		if s == nil {
			if frame != 1 || v.expressionSyntax == nil {
				return fmt.Errorf("stuntgres: missing stored query syntax")
			}
			ordinal := 0
			if err := v.validateLiteralTree(catalog, v.expressionSyntax, viewLiteralPath{Query: frame, Clause: analyze.ViewTargetLiteral}, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
				return err
			}
			continue
		}
		if s.Set != nil {
			set := querySetNode(analyze.ViewFromRoot(project))
			if set == nil || set.Left == nil || set.Right == nil || set.Left.Syntax != s.Set.Left || set.Right.Syntax != s.Set.Right || set.Operation != s.Set.Operator || set.All != s.Set.All {
				return fmt.Errorf("stuntgres: invalid frozen set syntax frames")
			}
			left, right := v.queries[s.Set.Left], v.queries[s.Set.Right]
			if left == nil || right == nil || left.Frame != set.Left.Frame || right.Frame != set.Right.Frame {
				return fmt.Errorf("stuntgres: invalid frozen set syntax frames")
			}
		}
		validate := func(raw parser.Expr, clause viewLiteralClause, target int) error {
			ordinal := 0
			return v.validateLiteralTree(catalog, raw, viewLiteralPath{Query: frame, Clause: clause, Target: target}, &ordinal, &count, &columnCount, &compositeCount, &routineCount)
		}
		for i, raw := range s.Targets {
			if _, star := raw.(parser.StarExpr); star {
				starCount++
				path := viewLiteralPath{Query: frame, Clause: analyze.ViewTargetLiteral, Target: i}
				columns, ok := v.stars[path]
				if !ok {
					return fmt.Errorf("stuntgres: missing frozen view star")
				}
				for _, column := range columns {
					owner := v.frames[column.Frame]
					if owner == nil {
						return fmt.Errorf("stuntgres: missing frozen star frame")
					}
					root := analyze.ViewFromRoot(owner)
					if column.Slot < 0 || column.Slot >= len(root.Schema()) {
						return fmt.Errorf("stuntgres: invalid frozen view star slot")
					}
				}
			}
			if err := validate(raw, analyze.ViewTargetLiteral, i); err != nil {
				return err
			}
		}
		if s.Distinct != nil {
			for i, raw := range s.Distinct.On {
				if err := validate(raw, analyze.ViewDistinctOnLiteral, i); err != nil {
					return err
				}
			}
		}
		if err := validate(s.Where, analyze.ViewWhereLiteral, 0); err != nil {
			return err
		}
		i := 0
		if err := s.GroupBy.VisitExpressions(func(raw parser.Expr) error {
			err := validate(raw, analyze.ViewGroupLiteral, i)
			i++
			return err
		}); err != nil {
			return err
		}
		if err := validate(s.Having, analyze.ViewHavingLiteral, 0); err != nil {
			return err
		}
		for i, definition := range s.Windows {
			ordinal := 0
			path := viewLiteralPath{Query: frame, Clause: analyze.ViewWindowLiteral, Target: i}
			for _, key := range definition.Spec.Partition {
				if err := v.validateLiteralTree(catalog, key, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
					return err
				}
			}
			for _, key := range definition.Spec.Order {
				if err := v.validateLiteralTree(catalog, key.Expr, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
					return err
				}
			}
			for _, offset := range definition.Spec.Offsets() {
				if err := v.validateLiteralTree(catalog, offset, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
					return err
				}
			}
		}
		// VALUES owns its decorations' source-ordinal literal paths even when
		// the SELECT syntax wrapper also exposes those same tail pointers.
		if s.Values == nil && s.Order != nil {
			for i, key := range s.Order.Keys {
				if err := validate(key.Expr, analyze.ViewOrderLiteral, i); err != nil {
					return err
				}
			}
		}
		if s.Values == nil && s.Bounds != nil {
			if err := validate(s.Bounds.Offset, analyze.ViewOffsetLiteral, 0); err != nil {
				return err
			}
			if err := validate(s.Bounds.Count, analyze.ViewLimitLiteral, 0); err != nil {
				return err
			}
		}
		var from func(parser.FromItem, plan.Node) error
		from = func(item parser.FromItem, node plan.Node) error {
			switch raw := item.(type) {
			case *parser.FunctionTable:
				table, ok := analyze.ViewFromRoot(node).(*plan.FunctionFromNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored string function frame")
				}
				for i, call := range parser.NormalizedFunctionCalls(raw) {
					if table.RegexpNamespace {
						path := viewLiteralPath{Query: frame, Clause: analyze.ViewFunctionLiteral, Source: table.ViewOrdinal, Target: i}
						ordinal := 0
						if err := v.validateLiteralTree(catalog, call, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
							return err
						}
					} else if err := validate(call, analyze.ViewFunctionLiteral, table.ViewOrdinal+i); err != nil {
						return err
					}
				}
				return nil
			case *parser.AliasReference:
				return from(raw.Input, node)
			case *parser.JoinReference:
				join, ok := node.(*plan.JoinNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored join frame")
				}
				if err := validate(raw.On, analyze.ViewJoinLiteral, join.ViewOrdinal); err != nil {
					return err
				}
				if err := from(raw.Left, join.Left); err != nil {
					return err
				}
				return from(raw.Right, join.Right)
			case *parser.ValuesQuery:
				values, ok := analyze.ViewFromRoot(node).(*plan.ValuesNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored VALUES frame")
				}
				return v.validateValuesLiterals(catalog, raw, frame, values.ViewOrdinal, &count, &columnCount, &compositeCount, &routineCount)
			case *parser.FunctionFrom:
				function, ok := analyze.ViewFromRoot(node).(*plan.FunctionFromNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored function frame")
				}
				for i, call := range raw.Calls {
					path := viewLiteralPath{Query: frame, Clause: analyze.ViewFunctionLiteral, Source: function.ViewOrdinal, Target: i}
					ordinal := 0
					if err := v.validateLiteralTree(catalog, call, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if s.Values != nil {
			if err := from(s.Values, analyze.ViewFromRoot(project)); err != nil {
				return err
			}
		} else if err := from(s.From, analyze.ViewFromRoot(project)); err != nil {
			return err
		}
	}
	if count != len(v.literals) || columnCount != len(v.columns) || starCount != len(v.stars) || compositeCount != len(v.composites) || routineCount != len(v.routineRefs) {
		return fmt.Errorf("stuntgres: invalid view literal occurrence path")
	}
	return nil
}

// Scalar stars and ordinary column syntax close over the same captured frame
// and slot, independently of composite expansion occurrences.
func (v *viewDefinition) validateColumnOccurrence(path viewLiteralPath) error {
	column, ok := v.columns[path]
	owner := v.frames[column.Frame]
	if !ok || owner == nil {
		return fmt.Errorf("stuntgres: invalid view column frame")
	}
	root := analyze.ViewFromRoot(owner)
	if path.Clause == analyze.ViewJoinLiteral && column.Frame == path.Query {
		root = viewJoinAt(root, path.Target)
	}
	if path.Clause == analyze.ViewValuesOrderLiteral && column.Frame == path.Query {
		root = viewValuesAt(owner, path.Source)
	}
	if root == nil || column.Slot < 0 || column.Slot >= len(root.Schema()) {
		return fmt.Errorf("stuntgres: invalid view column occurrence")
	}
	return nil
}

func (v *viewDefinition) validateLiteralTree(catalog catalog.Read, raw parser.Expr, path viewLiteralPath, ordinal, count, columnCount, compositeCount, routineCount *int) error {
	if raw == nil {
		return nil
	}
	path.Node = *ordinal
	(*ordinal)++
	if call, ok := raw.(parser.FunctionExpr); ok && call.StoredSQLRoutine {
		if _, err := v.sqlRoutineRecipeProcedure(catalog, path, call); err != nil {
			return err
		}
		(*routineCount)++
	} else if name := extension.IntaggRecipeFunction(raw); name != "" {
		if _, err := v.intaggRecipeProcedure(catalog, path, name); err != nil {
			return err
		}
		(*routineCount)++
	} else if _, exists := v.routineRefs[path]; exists {
		return fmt.Errorf("stuntgres: mismatched frozen routine occurrence")
	}
	child := func(raw parser.Expr) error {
		return v.validateLiteralTree(catalog, raw, path, ordinal, count, columnCount, compositeCount, routineCount)
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
		if !ok || r.Expansion && captured.Kind != analyze.CompositeExpansion || !r.Expansion && captured.Kind != analyze.CompositeSelection && captured.Kind != analyze.CompositeDotBuiltin {
			return fmt.Errorf("stuntgres: missing frozen field interpretation")
		}
		if captured.Kind == analyze.CompositeDotBuiltin && captured.Function != functions.NullCountFunctionKind(r.Field.Text) {
			return fmt.Errorf("stuntgres: invalid frozen dot provider")
		}
		if err := validateCompositeOccurrence(captured, catalog); err != nil {
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
			if err := validateRegroleStoredLiteral(literal.Value); err != nil {
				return err
			}
			usage := literal.ResultUsage()
			if !usage.Valid() || literal.Value.Type() != usage.Type {
				return fmt.Errorf("stuntgres: invalid view literal usage")
			}
			if err := validateUsageCollation(catalog, usage); err != nil {
				return err
			}
			// Contextual RECORD literals own occurrence layouts, not the
			// catalog's lookup-only RECORD. Concrete leaves still must match.
			if !validExpressionUsage(catalog, usage) {
				return fmt.Errorf("stuntgres: foreign view literal type")
			}
			if ref := literal.RegclassRef; ref != (ids.ObjectRef{}) {
				consumer, _ := catalog.Relation(v.consumer)
				if !storedViewReferenceValid(catalog, consumer, ref) || ref.Address.ClassID != ids.RelationClassID || ref.Address.SubID != 0 || usage.Type.ID() != sqltype.Regclass || literal.Value.IsNull() {
					return fmt.Errorf("stuntgres: invalid view regclass literal authority")
				}
			}
			(*count)++
			return nil
		}
		if integer, ok := raw.(parser.LiteralExpr); ok && path.Clause == analyze.ViewValuesOrderLiteral && path.Node == 0 && integer.Value.Kind == parser.IntegerValue {
			root := viewValuesAt(v.frames[path.Query], path.Source)
			if root == nil {
				return fmt.Errorf("stuntgres: missing VALUES order frame")
			}
			_, _, err := orderOrdinal(raw, len(root.Schema()), "ORDER BY")
			return err
		}
		if integer, ok := raw.(parser.LiteralExpr); ok && (path.Clause == analyze.ViewGroupLiteral || path.Clause == analyze.ViewOrderLiteral || path.Clause == analyze.ViewDistinctOnLiteral) && path.Node == 0 && integer.Value.Kind == parser.IntegerValue {
			if path.Clause != analyze.ViewGroupLiteral {
				clause := "ORDER BY"
				if path.Clause == analyze.ViewDistinctOnLiteral {
					clause = "DISTINCT ON"
				}
				_, _, err := orderOrdinal(raw, len(v.frames[path.Query].Schema()), clause)
				return err
			}
			_, _, err := groupOrdinal(raw, len(v.frames[path.Query].Schema()))
			return err
		}
		return fmt.Errorf("stuntgres: missing view literal occurrence")
	case parser.ColumnExpr:
		if captured, whole := v.composites[path]; whole {
			if captured.Kind != analyze.CompositeWholeRow {
				return fmt.Errorf("stuntgres: invalid frozen scalar whole row")
			}
			if err := validateCompositeOccurrence(captured, catalog); err != nil {
				return err
			}
			if err := v.validateCompositeSource(captured); err != nil {
				return err
			}
			if v.namespaces[captured.Source.Namespace-1].Qualifiers[captured.Source.Qualifier].Name != r.Name.Parts[len(r.Name.Parts)-1].Text {
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
			if !ok || captured.Kind != analyze.CompositeExpansion && captured.Kind != analyze.CompositeWholeRow {
				return fmt.Errorf("stuntgres: missing frozen whole row interpretation")
			}
			if err := validateCompositeOccurrence(captured, catalog); err != nil {
				return err
			}
			if err := v.validateCompositeSource(captured); err != nil {
				return err
			}
			if captured.Source.Namespace == 0 || v.namespaces[captured.Source.Namespace-1].Qualifiers[captured.Source.Qualifier].Name != r.Qualifier.Parts[len(r.Qualifier.Parts)-1].Text {
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
		d, ok := catalog.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)})
		if !ok {
			return fmt.Errorf("stuntgres: missing view collation identity")
		}
		if _, err := d.Usage(); err != nil {
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
			if captured.Kind != analyze.CompositeSelection || len(r.Args) != 1 {
				return fmt.Errorf("stuntgres: invalid frozen functional projection")
			}
			if err := validateCompositeOccurrence(captured, catalog); err != nil {
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

func (b *expressionBinder) captureSubqueryLeft(raw parser.Expr, n plan.SubqueryExpr, path viewLiteralPath, ordinal *int) error {
	if raw == nil {
		return nil
	}
	if constructor, ok := n.LeftConstructor.(plan.RowExpr); ok {
		row, ok := raw.(parser.RowExpr)
		if !ok {
			return fmt.Errorf("stuntgres: mismatched normalized quantified constructor")
		}
		path.Node = *ordinal
		(*ordinal)++
		return b.captureConstructorFields(row, len(constructor.Fields), func(i int) expr.Expr { return constructor.Fields[i] }, constructor.Expansions, true, path, ordinal)
	}
	if row, ok := raw.(parser.RowExpr); ok {
		(*ordinal)++
		return b.captureRowComparisonFields(row, n.Comparisons, 0, n.LeftExpansions, false, path, ordinal)
	}
	if len(n.Comparisons) != 1 {
		return fmt.Errorf("stuntgres: mismatched frozen scalar comparison")
	}
	return b.captureViewLiteralTree(raw, n.Comparisons[0].Left, path, ordinal)
}

func (b *expressionBinder) captureRowComparison(raw []parser.Expr, n plan.RowComparisonExpr, path viewLiteralPath, ordinal *int) error {
	if len(raw) != 2 {
		return fmt.Errorf("stuntgres: mismatched row comparison syntax")
	}
	for side, arg := range raw {
		row, ok := arg.(parser.RowExpr)
		if !ok {
			return fmt.Errorf("stuntgres: mismatched row comparison syntax")
		}
		path.Node = *ordinal
		(*ordinal)++ // The structural constructor has no scalar datum.
		if err := b.captureRowComparisonFields(row, n.Comparisons, side, n.Expansions[side], n.MemberCasts[side], path, ordinal); err != nil {
			return err
		}
	}
	return nil
}

func (b *expressionBinder) captureRowComparisonFields(row parser.RowExpr, pairs []plan.ComparisonExpr, side int, expansions map[int]plan.CompositeExpansionBinding, memberCasts bool, path viewLiteralPath, ordinal *int) error {
	path.Argument = 0
	return b.captureConstructorFields(row, len(pairs), func(i int) expr.Expr {
		if side == 1 {
			return pairs[i].Right
		}
		return pairs[i].Left
	}, expansions, memberCasts, path, ordinal)
}
