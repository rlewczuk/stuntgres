package analyze

import (
	"fmt"
	"stuntgres/internal/engine/catalog"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) CaptureViewLiterals(raw parser.Expr, bound expr.Expr, clause ViewLiteralClause, target int) error {
	path := ViewLiteralPath{Query: b.FrameID(), Clause: clause, Target: target}
	ordinal := 0
	return b.CaptureViewLiteralTree(raw, bound, path, &ordinal)
}

func (b *Binder) CaptureViewLiteralTree(raw parser.Expr, bound expr.Expr, path ViewLiteralPath, ordinal *int) error {
	return b.captureNormalizedViewLiteralTree(b.NormalizeStoredExpression(raw), bound, path, ordinal)
}

func (b *Binder) captureNormalizedViewLiteralTree(raw parser.Expr, bound expr.Expr, path ViewLiteralPath, ordinal *int) error {
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
			if b.Capture.PosixSubstrings == nil {
				b.Capture.PosixSubstrings = make(map[ViewLiteralPath]bool)
			}
			b.Capture.PosixSubstrings[path] = true
		}
		if operands.SimilarSubstring {
			if b.Capture.SimilarSubstrings == nil {
				b.Capture.SimilarSubstrings = make(map[ViewLiteralPath]bool)
			}
			b.Capture.SimilarSubstrings[path] = true
		}
		if operands.DotBuiltin != "" {
			base, _ := operands.At(hook, 0)
			b.Capture.CaptureViewComposite(path, CompositeDotBuiltin, base, nil)
			captured := b.Capture.Composites[path]
			captured.Function = functions.NullCountFunctionKind(operands.DotBuiltin)
			b.Capture.Composites[path] = captured
		}
		for i, written := range raw {
			arg, _ := operands.At(hook, i) // Check validated every slot before any map write.
			if operands.SelectedImplicit || operands.CallImplicit {
				if coercion, ok := arg.(plan.CoercionExpr); ok && coercion.Mode == sqltype.Implicit {
					if b.Capture.CastUsages == nil {
						b.Capture.CastUsages = make(map[ViewLiteralPath]sqltype.Usage)
					}
					key := path
					if operands.SelectedImplicit {
						key.Argument = i + 1
					}
					b.Capture.CastUsages[key] = coercion.Usage
				}
			}
			if operands.JSONSources {
				arg = plan.SQLJSONSource(arg)
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
		if parameter, ok := bound.(plan.ParameterExpr); b.Consumer.frozenDefinition && ok && parameter.Number == r.Number {
			return nil
		}
	case parser.InListExpr:
		if n, ok := bound.(plan.ScalarArrayComparisonExpr); ok && n.Array == nil {
			if err := child(r.Left, n.Left); err != nil {
				return err
			}
			return children(r.Items, n.Items)
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
	case parser.SQLJSONQueryExpr:
		if r.Returning != nil {
			if b.Capture.CastUsages == nil {
				b.Capture.CastUsages = make(map[ViewLiteralPath]sqltype.Usage)
			}
			b.Capture.CastUsages[path] = bound.ResultUsage()
		}
		return protocol(expr.CaptureSQLJSON, r.Children())
	case parser.SQLJSONExpr:
		if r.Returning != nil {
			if b.Capture.CastUsages == nil {
				b.Capture.CastUsages = make(map[ViewLiteralPath]sqltype.Usage)
			}
			b.Capture.CastUsages[path] = bound.ResultUsage()
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
			if err := ValidateRegroleStoredLiteral(literal.Value); err != nil {
				return err
			}
			if b.Capture.Literals == nil {
				b.Capture.Literals = make(map[ViewLiteralPath]plan.LiteralExpr)
			}
			b.Capture.Literals[path] = literal
			return nil
		}
	case parser.ColumnExpr, parser.StarExpr:
		if column, ok := bound.(plan.ColumnExpr); ok {
			if b.Capture.Columns == nil {
				b.Capture.Columns = make(map[ViewLiteralPath]ViewColumn)
			}
			captured := ViewColumn{Frame: column.Frame, Slot: column.Slot}
			if column.Scope == RulePseudoScope {
				captured.RuleRef = column.Origin
				captured.RuleSide = column.Frame
			}
			b.Capture.Columns[path] = captured
			return nil
		}
		if row, ok := bound.(plan.RowExpr); ok && row.WholeNames != nil {
			b.Capture.CaptureViewComposite(path, CompositeWholeRow, row, nil)
			return nil
		}
	case parser.GroupingExpr:
		if n, ok := bound.(plan.GroupingExpr); ok {
			return children(r.Args, n.Args)
		}
	case parser.AggregateExpr:
		if r.JSON != nil && r.JSON.Returning != nil {
			if b.Capture.CastUsages == nil {
				b.Capture.CastUsages = make(map[ViewLiteralPath]sqltype.Usage)
			}
			b.Capture.CastUsages[path] = bound.ResultUsage()
			if conversion, ok := bound.(plan.CoercionExpr); ok {
				bound = conversion.Child
			}
		}
		if n, ok := bound.(*plan.WindowExpr); ok && r.Window != nil {
			if n.Call < 0 || n.Call >= len(b.Grouping.WindowCalls) {
				return fmt.Errorf("stuntgres: invalid stored window occurrence")
			}
			call := b.Grouping.WindowCalls[n.Call]
			if call.Procedure == nil {
				return fmt.Errorf("stuntgres: missing captured window declaration")
			}
			if err := b.captureProviderReference(path, r, call.Procedure, call.Routine); err != nil {
				return err
			}
			if call := b.Grouping.WindowCalls[n.Call]; call.Argument != nil {
				if err := child(r.Args[0], call.Argument); err != nil {
					return err
				}
			}
			if call := b.Grouping.WindowCalls[n.Call]; call.Ordinal != nil {
				if err := child(r.Args[1], call.Ordinal); err != nil {
					return err
				}
			}
			if call := b.Grouping.WindowCalls[n.Call]; call.DefaultValue != nil {
				if err := child(r.Args[2], call.DefaultValue); err != nil {
					return err
				}
			}
			spec := b.Grouping.WindowCalls[n.Call].Spec
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
				if call.Procedure != nil && call.Procedure.Builtin == nil {
					if call.Procedure.DynamicCallable() && call.Procedure.Aggregate != nil {
						if !r.StoredAggregate {
							return fmt.Errorf("stuntgres: missing normalized aggregate syntax")
						}
						read := b.BindingCatalog()
						if call.Ref != b.Host.CatalogOwner().Domain.Ref(read.Epoch(), call.Procedure.Address()) {
							return fmt.Errorf("stuntgres: invalid frozen aggregate reference")
						}
						if _, err := exec.ValidateStoredRoutineDescriptor(read, call.Procedure); err != nil {
							return err
						}
						if err := b.Capture.CaptureRoutineReference(path, call.Ref); err != nil {
							return err
						}
					} else if extension.IntaggProviderOf(call.Procedure) != nil {
						if err := b.captureIntaggReference(path, call.Procedure, call.Ref, "int_array_aggregate"); err != nil {
							return err
						}
						if err := b.captureProviderReference(path, r, call.Procedure, call.Ref); err != nil {
							return err
						}
					} else if err := b.captureProviderReference(path, r, call.Procedure, call.Ref); err != nil {
						return err
					}
				}
				if call.Procedure != nil && call.Procedure.Builtin != nil {
					p, ref := call.Procedure, call.Routine
					// Select the written occurrence only from the independently
					// captured declarations sharing this computation.
					for _, alias := range call.RoutineAliases {
						candidate := alias.Procedure
						if candidate != nil && (candidate.Name() == r.Name || candidate.Builtin != nil && candidate.Builtin.Identity.Name == r.Name) {
							p, ref = candidate, alias.Ref
							break
						}
					}
					if err := b.captureProviderReference(path, r, p, ref); err != nil {
						return err
					}
				}
				for i, arg := range r.Args {
					var expression expr.Expr
					if r.Within {
						if i >= len(call.Direct) {
							return fmt.Errorf("stuntgres: invalid stored direct arguments")
						}
						expression = call.Direct[i]
					} else {
						if i >= len(call.Arguments) {
							return fmt.Errorf("stuntgres: invalid stored aggregate arguments")
						}
						expression = call.Arguments[i]
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
			b.Capture.CaptureViewComposite(path, CompositeSelection, n.Base, []int32{n.Attribute})
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
			if b.Capture.Collations == nil {
				b.Capture.Collations = make(map[ViewLiteralPath]uint32)
			}
			b.Capture.Collations[path] = n.Usage.Collation.ID
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
		if b.Capture.CastUsages == nil {
			b.Capture.CastUsages = make(map[ViewLiteralPath]sqltype.Usage)
		}
		b.Capture.CastUsages[path] = bound.ResultUsage()
		if coercion, ok := bound.(plan.CoercionExpr); ok && coercion.Mode == sqltype.Explicit {
			bound = coercion.Child
		}
		if coercion, ok := bound.(plan.DatumCastExpr); ok {
			bound = coercion.Child
		}
		if coercion, ok := bound.(plan.ArrayCastExpr); ok {
			bound = coercion.Child
		}
		bound = RestoreDomainCastCollations(annotations, bound)
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
			if b.Capture.OperatorRefs == nil {
				b.Capture.OperatorRefs = make(map[ViewLiteralPath]ids.ObjectRef)
			}
			b.Capture.OperatorRefs[path] = n.Ref
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
		if n, ok := bound.(plan.RangeConstructorExpr); ok {
			if !r.StoredSQLRoutine {
				return fmt.Errorf("missing normalized constructor syntax")
			}
			if err := n.ValidateStoredProvider(); err != nil {
				return err
			}
			if err := b.Capture.CaptureRoutineReference(path, n.Ref); err != nil {
				return err
			}
			return protocol(expr.CaptureFunction, r.Args)
		}
		if n, ok := bound.(plan.SQLRoutineExpr); ok {
			if !r.StoredSQLRoutine {
				return fmt.Errorf("stuntgres: missing normalized SQL routine syntax")
			}
			if err := b.captureSQLRoutineReference(path, n); err != nil {
				return err
			}
			return protocol(expr.CaptureFunction, r.Args)
		}
		if p, err := exec.ExpressionRoutineDescriptor(bound); err != nil {
			return err
		} else if p != nil {
			ref := b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), p.Address())
			if err := b.captureProviderReference(path, r, p, ref); err != nil {
				return err
			}
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
		if n, ok := bound.(plan.InstalledPgrowlocksExpr); ok {
			if err := extension.ValidatePgrowlocksIdentity(b.BindingCatalog(), n.Procedure, n.Ref); err != nil {
				return err
			}
			if err := b.Capture.CaptureRoutineReference(path, n.Ref); err != nil {
				return err
			}
			return children(r.Args, []expr.Expr{n.Arg})
		}
		if n, ok := bound.(plan.InstalledEarthExpr); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.InstalledCubeExpr); ok {
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
			if len(r.ArgNames) == 0 {
				return children(r.Args, n.Args)
			}
			member, ok := functions.UUIDOsspLookupSelector(n.Name)
			if !ok || len(r.Args) != 2 || len(n.Args) != 2 {
				return fmt.Errorf("stuntgres: mismatched named UUID arguments")
			}
			var lexical [2]expr.Expr
			for i := range r.Args {
				slot := i
				if i < len(r.ArgNames) && r.ArgNames[i].Text != "" {
					for j, name := range member.ArgumentNames {
						if name == r.ArgNames[i].Text {
							slot = j
							break
						}
					}
				}
				lexical[i] = n.Args[slot]
			}
			return children(r.Args, lexical[:])
		}
		if n, ok := bound.(plan.InstalledCryptoExpr); ok {
			return children(r.Args, n.Args)
		}
		if n, ok := bound.(plan.InstalledTextExpr); ok {
			return children(r.Args, []expr.Expr{n.Arg})
		}
		if n, ok := bound.(plan.FieldExpr); ok && len(r.Args) == 1 {
			b.Capture.CaptureViewComposite(path, CompositeSelection, n.Base, []int32{n.Attribute})
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
				visible, err := b.builtinVisible(r.Name, FunctionLookup)
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

func (b *Binder) captureSubqueryLeft(raw parser.Expr, n plan.SubqueryExpr, path ViewLiteralPath, ordinal *int) error {
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
	return b.CaptureViewLiteralTree(raw, n.Comparisons[0].Left, path, ordinal)
}

func (b *Binder) captureRowComparison(raw []parser.Expr, n plan.RowComparisonExpr, path ViewLiteralPath, ordinal *int) error {
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

func (b *Binder) captureRowComparisonFields(row parser.RowExpr, pairs []plan.ComparisonExpr, side int, expansions map[int]plan.CompositeExpansionBinding, memberCasts bool, path ViewLiteralPath, ordinal *int) error {
	path.Argument = 0
	return b.captureConstructorFields(row, len(pairs), func(i int) expr.Expr {
		if side == 1 {
			return pairs[i].Right
		}
		return pairs[i].Left
	}, expansions, memberCasts, path, ordinal)
}

// Provider occurrences reuse the existing checked routine-ref map and syntax
// normalization. A separate immutable symbol checks capture substitution;
// written names and syntax stay intact, while relabels follow the captured address.
func (b *Binder) captureProviderReference(path ViewLiteralPath, raw parser.Expr, p *catalog.Procedure, ref ids.ObjectRef) error {
	if p.Builtin == nil && (p.Implementation == nil || p.Extension == (ids.ObjectAddress{})) {
		return nil
	}
	read := b.BindingCatalog()
	if ref != b.Host.CatalogOwner().Domain.Ref(read.Epoch(), p.Address()) {
		return fmt.Errorf("stuntgres: invalid frozen provider reference")
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(read, p); err != nil {
		return err
	}
	symbol := ""
	if p.Builtin != nil {
		if routine.BuiltinRoutine(p.Address().ObjectID) != p.Builtin {
			return fmt.Errorf("stuntgres: invalid frozen builtin association")
		}
		symbol = p.Builtin.Identity.Name
	} else {
		var err error
		symbol, err = extension.ProcedureSymbol(read, p)
		if err != nil {
			return err
		}
	}
	var normalized parser.Expr
	switch call := raw.(type) {
	case parser.FunctionExpr:
		call.StoredProviderRoutine, call.StoredProviderSymbol = true, symbol
		normalized = call
	case parser.AggregateExpr:
		call.StoredProviderRoutine, call.StoredProviderSymbol = true, symbol
		normalized = call
	default:
		return fmt.Errorf("stuntgres: invalid frozen provider syntax")
	}
	if err := b.Capture.CaptureRoutineReference(path, ref); err != nil {
		return err
	}
	if b.Stored.Syntax == nil {
		b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
	}
	b.Stored.Syntax[b.storedExpressionSyntaxKey(raw)] = normalized
	return nil
}
