package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func functionCastError(raw parser.CastExpr, source string, ambiguous bool) error {
	code, ending, hint := "42883", "does not exist", functions.SignatureHint
	if ambiguous {
		code, ending = "42725", "is not unique"
		hint = "Could not choose a best candidate function. You might need to add explicit type casts."
	}
	name := raw.Callee
	if raw.TypeName.Schema != nil {
		name = raw.TypeName.Schema.Text + "." + name
	}
	d := expr.ErrorAt(code, fmt.Sprintf("function %s(%s) %s", name, source, ending), raw.Pos).(*sqlstate.Error)
	d.Hint = hint
	return d
}

func (b *Binder) Cast(raw parser.CastExpr, outerPolicy ExpressionPolicy) (expr.Expr, error) {
	return b.cast(raw, outerPolicy, nil, nil)
}

// The optional witness belongs only to selected argument analysis. The ordinary
// cast path remains identical and no lexical flag can escape into sibling calls.
func (b *Binder) cast(raw parser.CastExpr, outerPolicy ExpressionPolicy, witness **functions.TypeInquiryUnknownArgument, stringIO **sqltype.Descriptor) (expr.Expr, error) {
	rawNull, isNull := raw.Child.(parser.LiteralExpr)
	unknownNull := raw.Syntax != parser.FunctionCast && isNull && rawNull.Value.Kind == parser.NullValue
	var target sqltype.Usage
	var err error
	var boundChild expr.Expr
	var childUnknown *functions.TypeInquiryUnknownArgument
	var childIO *sqltype.Descriptor
	// Selected recursion resolves each child cast before its enclosing target.
	// The bound child is reused even when the outer target is not transparent.
	if childCast, ok := raw.Child.(parser.CastExpr); ok && witness != nil && stringIO != nil {
		boundChild, err = b.cast(childCast, outerPolicy, &childUnknown, &childIO)
		if err != nil {
			return nil, err
		}
	}
	annotation, err := b.castAnnotation(raw)
	if err != nil {
		return nil, err
	}
	if outerPolicy.thresholdArrayInput {
		target, err = b.Host.ResolveAnnotationTypeInput(annotation, unknownNull, false, false, true)
	} else {
		target, err = b.Host.ResolveAnnotationType(annotation, unknownNull)
	}
	if err != nil {
		switch raw.Child.(type) {
		case parser.LiteralExpr:
			return nil, err
		}
		if boundChild == nil {
			boundChild, _ = b.Bind(raw.Child, ExpressionPolicy{Context: ProjectionContext})
		}
		if boundChild == nil || boundChild.ResultUsage().Type == nil {
			return nil, err
		}
		resolved, identityErr := b.resolveCastIdentity(annotation, boundChild.ResultUsage().Type)
		if identityErr != nil {
			return nil, err
		}
		target = resolved
		err = nil
	}
	// Selected analysis owns the child once, including its logical authority.
	// Redundant casts extend provenance, never the executable evaluator graph.
	if boundChild != nil {
		if (childUnknown != nil || childIO != nil) && expr.TransparentUnknownTarget(target.Type) {
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.Type.ID())}
			descriptor, ok := b.definitionDescriptor(address)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing composed unknown cast authority")
			}
			b.dependency(b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), address), descriptor.Revision())
			step := expr.TransparentInputCast{Syntax: raw, Target: target.Type}
			if childUnknown != nil {
				count := max(1, len(childUnknown.Chain))
				steps := make([]expr.TransparentInputCast, count+1)
				if len(childUnknown.Chain) == 0 {
					steps[0] = expr.TransparentInputCast{Syntax: childUnknown.Raw.(parser.CastExpr), Target: childUnknown.CastType}
				} else {
					copy(steps, childUnknown.Chain)
				}
				steps[count] = step
				*witness = &functions.TypeInquiryUnknownArgument{Literal: childUnknown.Literal, Raw: raw, CastType: target.Type, Chain: steps}
			} else {
				coercion, ok := boundChild.(plan.CoercionExpr)
				if !ok {
					return nil, fmt.Errorf("stuntgres: composed unknown IO lacks transport")
				}
				if _, err := coercion.LogicalInputCast(); err != nil {
					return nil, err
				}
				marker := *coercion.UnknownInputCast
				marker.Transparent = make([]expr.TransparentInputCast, len(marker.Transparent)+1)
				copy(marker.Transparent, coercion.UnknownInputCast.Transparent)
				marker.Transparent[len(marker.Transparent)-1] = step
				coercion.UnknownInputCast = &marker
				boundChild, *stringIO = coercion, childIO
			}
			return boundChild, nil
		}
	}
	if target.Type.ID() == 2281 {
		source := "unknown"
		if !isNull || rawNull.Value.Kind != parser.NullValue && rawNull.Value.Kind != parser.StringValue {
			child := boundChild
			var bindErr error
			if child == nil {
				child, bindErr = b.Bind(raw.Child, ExpressionPolicy{Context: ProjectionContext})
			}
			if bindErr != nil {
				return nil, bindErr
			}
			source = b.DiagnosticTypeName(child.ResultUsage().Type)
		}
		return nil, expr.ErrorAt("42846", "cannot cast type "+source+" to internal", raw.Pos)
	}
	if literal, ok := raw.Child.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.NullValue || literal.Value.Kind == parser.StringValue) {
		switch target.Type.ID() {
		case 2276, 2283, 2776, 5077, 5079:
			node, err := b.Bind(raw.Child, outerPolicy)
			if err == nil && witness != nil {
				address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.Type.ID())}
				descriptor, ok := b.definitionDescriptor(address)
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing unknown cast type authority")
				}
				b.dependency(b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), address), descriptor.Revision())
				*witness = &functions.TypeInquiryUnknownArgument{Literal: literal, CastType: target.Type, Raw: raw}
			}
			return node, err
		case 3500:
			return nil, expr.ErrorAt("42846", "cannot cast type unknown to anyenum", raw.Pos)
		case 269, 325, 2280, 3115, 3310, 2279, 3838:
			return nil, expr.ErrorAt("0A000", "cannot accept a value of type "+target.Type.Name(), literal.Value.Pos)
		}
		if literal.Value.Kind == parser.NullValue && target.QueryNullUsage() {
			return plan.LiteralExpr{Info: b.Info(target, raw.Pos, expr.Properties{}), Value: sqltype.Null(target.Type)}, nil
		}
	}
	if literal, ok := raw.Child.(parser.LiteralExpr); ok && literal.Value.Kind == parser.StringValue && sqltype.IsPseudo(target.Type.ID()) && target.Type.ID() != sqltype.Void && target.Type.ID() != sqltype.Unknown && target.Type.ID() != 2275 && target.Type.ID() != 2249 {
		return nil, expr.ErrorAt("0A000", "cannot accept a value of type "+target.Type.Name(), literal.Value.Pos)
	}
	if b.Clause.JSONBRecordBase && raw.Syntax != parser.FunctionCast && target.Type.IsUserDefined() && target.Type.BaseType().IsComposite() {
		b.Prepared.CaptureComposite(PreparedTypeKey{Offset: raw.TypeName.Pos.Offset, Kind: CompositeRecordArgument}, target, nil)
	}
	if (target.Type.IsUserDefined() || target.Type.IsDomain() || target.Type.IsArray() && (target.Type.Element().IsUserDefined() || target.Type.Element().IsDomain())) && raw.Syntax != parser.FunctionCast {
		if b.Prepared.Types == nil {
			b.Prepared.Types = make(map[PreparedTypeKey]TypeOccurrence)
		}
		kind := IdentityCast
		if target.Type.IsDomain() || target.Type.IsArray() && target.Type.Element().IsDomain() {
			kind = DomainCast
		}
		key := PreparedTypeKey{Offset: raw.TypeName.Pos.Offset, Kind: kind}
		b.Prepared.Types[key] = TypeOccurrence{Kind: kind, Base: target}
	}
	if target.Type.ID() == sqltype.Unknown {
		// Selected type inquiries can retain a genuine string-category IO
		// cast without constructing an executable UNKNOWN value.
		source := ComparisonOperand{Node: boundChild, raw: raw.Child}
		if boundChild == nil {
			source, err = b.comparisonOperand(raw.Child)
			if err != nil {
				return nil, err
			}
		}
		if typ := source.descriptor(); typ != nil {
			if stringIO != nil {
				transport, err := sqltype.PlanUnknownInputTransport(source.Node.ResultUsage(), b.Type(sqltype.Text).Usage())
				if err == nil {
					address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.Type.ID())}
					descriptor, ok := b.definitionDescriptor(address)
					if !ok {
						return nil, fmt.Errorf("stuntgres: missing unknown IO target authority")
					}
					b.dependency(b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), address), descriptor.Revision())
					info := b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, CoercionProperties(source.Node, transport))
					*stringIO = target.Type
					return plan.CoercionExpr{Info: info, Child: source.Node, Plan: transport, Mode: sqltype.Explicit, UnknownInputCast: &plan.UnknownInputCast{Source: source.Node.ResultUsage(), Target: target.Type, Syntax: raw}}, nil
				}
			}
			if _, parameter := raw.Child.(parser.ParameterExpr); parameter && b.Expected.Held {
				// The accepted recipe already admitted this UNKNOWN no-op before
				// output inference. Its retained parameter type is not a new cast.
				return b.Bind(raw.Child, outerPolicy)
			}
			if raw.Syntax == parser.FunctionCast {
				return nil, functionCastError(raw, b.DiagnosticTypeName(typ), false)
			}
			return nil, expr.ErrorAt("42846", "cannot cast type "+b.DiagnosticTypeName(typ)+" to unknown", raw.Pos)
		}
		node := boundChild
		if node == nil {
			node, err = b.Bind(raw.Child, outerPolicy)
		}
		if literal, ok := raw.Child.(parser.LiteralExpr); err == nil && witness != nil && ok && (literal.Value.Kind == parser.NullValue || literal.Value.Kind == parser.StringValue) {
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.Type.ID())}
			descriptor, ok := b.definitionDescriptor(address)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing unknown cast type authority")
			}
			b.dependency(b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), address), descriptor.Revision())
			*witness = &functions.TypeInquiryUnknownArgument{Literal: literal, CastType: target.Type, Raw: raw}
		}
		return node, err
	}
	if row, ok := raw.Child.(parser.RowExpr); ok && target.Type.BaseType().IsComposite() && target.Type.ID() != 2249 {
		return b.BindRow(row, target, sqltype.Explicit, raw.Pos)
	}
	if literal, ok := raw.Child.(parser.LiteralExpr); ok && target.Type.ID() == sqltype.Regclass && literal.Value.Kind == parser.StringValue {
		node, _, err := b.bindRegclass(raw.Child)
		return node, err
	}
	if err := b.CaptureTypeDiagnosticInput(target.Type); err != nil {
		return nil, err
	}
	if target.Type.ContainsCatalogIdentifier() {
		if err := b.captureElementInput(); err != nil {
			return nil, err
		}
	}
	policy := ExpressionPolicy{Context: ProjectionContext, thresholdArrayInput: outerPolicy.thresholdArrayInput}
	switch child := raw.Child.(type) {
	case parser.LiteralExpr:
		if child.Value.Kind == parser.StringValue || child.Value.Kind == parser.NullValue {
			policy.Expected = target
			if target.Type.IsDomain() {
				policy.Expected = target.Type.BaseType().Usage()
				if policy.Expected.Type.ID() == sqltype.Interval {
					policy.Expected = target.BaseUsage()
				}
			}
		}
	case parser.ArrayExpr:
		policy.Expected = target
		policy.Mode = sqltype.Explicit
	case parser.CastExpr:
		if unknownNullSyntax(child) {
			policy.Expected = target
		}
	case parser.ParameterExpr:
		if b.PL != nil {
			break
		}
		if b.Stored.CheckLiterals != nil {
			break // bind resolves the captured literal without parameter inference.
		}
		if b.Params.NoParameters || b.Consumer.DefaultConsumer {
			return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
		}
		if b.Params.Parameters[child.Number].Type == nil {
			if raw.Syntax == parser.FunctionCast && target.Type.ID() != sqltype.Text {
				if child.Number <= 0 {
					return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
				}
				return nil, functionCastError(raw, "unknown", true)
			}
			policy.Expected = target
		}
	}
	child := boundChild
	if child == nil {
		child, err = b.Bind(raw.Child, policy)
	}
	if err != nil {
		return nil, err
	}
	// Logical UNKNOWN can use TEXT physically without having exited to TEXT.
	// Keep the explicit boundary and attach raw authority to its inner literal,
	// not to the concrete result. Genuine IO already owns its inner authority.
	textExit := target.Type.ID() == sqltype.Text && (childUnknown != nil || childIO != nil)
	if textExit && childUnknown != nil {
		child, err = typeInquiryUnknownTransport(child, childUnknown)
		if err != nil {
			return nil, err
		}
	}
	// Bind annotations before moving them so their original type checks,
	// locations, captured identities and child effects remain authoritative.
	child, annotations := DomainCoercionInput(child, target)
	source := child.ResultUsage()
	if recordCastPassthrough(source.Type, target.Type) {
		return RestoreDomainCastCollations(annotations, child), nil
	}
	if unknownNull && target.Type.IsDeferredRecord() && source == target {
		// This is NULL transport, not a general RECORD cast/coercion edge.
		return child, nil
	}
	pos := raw.Pos
	if start := child.SourcePosition(); start.Character > 0 && (pos.Character <= 0 || start.Character < pos.Character) {
		pos = start
	}
	info, err := b.coercionInfo(b.Info(target, pos, child.Properties()), child)
	if err != nil {
		return nil, err
	}
	target = info.Usage
	if raw.FrozenMethod != "" {
		if raw.FrozenMethod == "i" && info.Property.Volatility < expr.Stable {
			info.Property.Volatility = expr.Stable
		}
		n, err := plan.NewDatumCast(info, child, raw.FrozenMethod, sqltype.Explicit)
		n.Context = raw.FrozenContext
		return RestoreDomainCastCollations(annotations, n), err
	}
	if selected, handled, selectionErr := b.declaredCoercion(child, target, sqltype.Explicit, raw.Pos, raw.Child); handled {
		if n, ok := selected.(plan.DatumCastExpr); ok && (b.Params.NoParameters || b.Consumer.frozenDefinition) {
			frozen := raw
			frozen.FrozenMethod = n.Method
			frozen.FrozenContext = n.Context
			key := StoredExpressionSyntaxKey{Frame: b.FrameID(), Position: raw.Pos}
			if lowered, ok := b.Stored.Syntax[key]; ok {
				frozen.Child = lowered
			}
			if b.Stored.Syntax == nil {
				b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
			}
			b.Stored.Syntax[StoredExpressionSyntaxKey{Frame: b.FrameID(), Position: raw.Pos}] = frozen
		}
		if _, ok := selected.(plan.CoercionExpr); ok && (b.Params.NoParameters || b.Consumer.frozenDefinition) {
			key := StoredExpressionSyntaxKey{Frame: b.FrameID(), Position: raw.Pos}
			if lowered, ok := b.Stored.Syntax[key]; ok {
				frozen := raw
				frozen.Child = lowered
				b.Stored.Syntax[key] = frozen
			}
		}
		return RestoreDomainCastCollations(annotations, selected), selectionErr
	}
	coercionPlan, err := sqltype.PlanCoercion(source, target, sqltype.Explicit)
	if err != nil {
		if raw.Syntax == parser.FunctionCast {
			return nil, functionCastError(raw, source.Type.DiagnosticName(), false)
		}
		return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.DiagnosticTypeName(source.Type), b.DiagnosticTypeName(target.Type)), raw.Pos)
	}
	if coercionPlan.RequiresCatalogOutput() {
		b.Prepared.RegclassConcat = true
	}
	if source == target && !textExit {
		return RestoreDomainCastCollations(annotations, child), nil
	}
	info.Property = CoercionProperties(child, coercionPlan)
	if folded, ok, err := b.foldRegtypeConstant(child, coercionPlan, info); ok || err != nil {
		return RestoreDomainCastCollations(annotations, folded), err
	}
	return RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: child, Plan: coercionPlan, Mode: sqltype.Explicit}), nil
}
