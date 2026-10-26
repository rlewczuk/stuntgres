package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Unknown state exists only during analysis. It never becomes an executable Value.
type ComparisonOperand struct {
	Node expr.Expr
	raw  parser.Expr
}

func (o ComparisonOperand) descriptor() *sqltype.Descriptor {
	if o.Node == nil {
		return nil
	}
	return o.Node.ResultUsage().Type
}

// operandArgument hands an analyzed comparison operand to functions bind
// code; CoerceArgument rebuilds the operand from Expression and Raw.
func operandArgument(o ComparisonOperand) functions.Argument {
	argument := functions.Argument{Expression: o.Node, Raw: o.raw}
	if o.Node != nil {
		argument.Usage = o.Node.ResultUsage()
	}
	return argument
}

func (b *Binder) comparisonOperand(raw parser.Expr) (ComparisonOperand, error) {
	return b.comparisonOperandPolicy(raw, ExpressionPolicy{Context: ProjectionContext})
}

// AnalyzeArgument retains generic binding and occurrence-owned unknown inference.
// The request is closed: children cannot invoke catalog or parameter services.
func (b *Binder) AnalyzeArgument(raw parser.Expr, request functions.AnalysisRequest) (functions.Argument, error) {
	var operand ComparisonOperand
	var err error
	switch request.Intent {
	case functions.ComparisonArgument:
		operand, err = b.comparisonOperand(raw)
	case functions.ThresholdArrayArgument:
		operand, err = b.comparisonOperandPolicy(raw, ExpressionPolicy{Context: ProjectionContext, thresholdArrayInput: true})
	case functions.ZoneArgument:
		// Only explicit casts read the flag; restoring it keeps nested and
		// adjacent calls unaffected.
		previous := b.Consumer.DatetimeZoneOperands
		b.Consumer.DatetimeZoneOperands = true
		operand, err = b.comparisonOperand(raw)
		b.Consumer.DatetimeZoneOperands = previous
	case functions.StringTextArgument, functions.StringOtherArgument:
		operand, err = b.StringOperand(raw, request.Intent == functions.StringTextArgument)
	case functions.RecordBaseArgument:
		previous := b.Clause.JSONBRecordBase
		b.Clause.JSONBRecordBase = true
		operand, err = b.StringOperand(raw, false)
		b.Clause.JSONBRecordBase = previous
	case functions.StringProjectionArgument, functions.ProjectionArgument:
		operand.Node, err = b.Bind(raw, ExpressionPolicy{Context: ProjectionContext})
		operand.raw = raw
		if err == nil && unknownNullSyntax(raw) {
			if literal, ok := operand.Node.(plan.LiteralExpr); ok {
				literal.UnknownNull = true
				operand.Node = literal
			}
		}
	case functions.BareProjectionArgument:
		operand.Node, err = b.Bind(raw, ExpressionPolicy{Context: ProjectionContext})
		operand.raw = raw
	case functions.DirectDeferredArgument:
		if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
			operand.raw = raw
		} else {
			operand.Node, err = b.Bind(raw, ExpressionPolicy{Context: ProjectionContext})
			operand.raw = raw
		}
	default:
		return functions.Argument{}, fmt.Errorf("stuntgres: invalid function argument analysis")
	}
	original, _ := b.OriginalArgument(operand.raw)
	argument := functions.Argument{Expression: operand.Node, Raw: original}
	if operand.Node != nil {
		argument.Usage = operand.Node.ResultUsage()
		if argument.Usage.Type == nil {
			argument.Kind = functions.UnresolvedParameterArgument
		}
	} else if literal, ok := operand.raw.(parser.LiteralExpr); ok && literal.Value.Kind == parser.NullValue {
		argument.Kind = functions.DeferredNullArgument
	} else {
		argument.Kind = functions.DeferredStringArgument
	}
	return argument, err
}

func (b *Binder) CoerceArgument(argument functions.Argument, request functions.CoercionRequest) (expr.Expr, error) {
	operand := ComparisonOperand{Node: argument.Expression, raw: argument.Raw}
	// Already-bound operands need not have syntax (for example, an operator
	// supplied through a private slot). Retain their own source position.
	var pos parser.Position
	if argument.Expression != nil {
		pos = argument.Expression.SourcePosition()
	}
	if argument.Raw != nil {
		pos = argument.Raw.SourcePosition()
	}
	switch request.Intent {
	case functions.FinalizeArgument:
		return b.finalizeOperand(operand, request.Target.Type)
	case functions.LikeArgument:
		return b.likeOperand(operand, request.Target.Type)
	case functions.RecordFormalArgument:
		var record *sqltype.Descriptor
		if parameter, ok := argument.Expression.(plan.ParameterExpr); ok {
			record = b.Params.Parameters[parameter.Number].Type
		}
		if record == nil {
			var err error
			record, err = sqltype.RecordShape(nil)
			if err != nil {
				return nil, err
			}
		}
		return b.finalizeOperand(operand, record)
	case functions.SizeFormalArgument:
		node, err := b.finalizeOperand(operand, request.Target.Type)
		if err != nil {
			return nil, err
		}
		if node.ResultUsage() == request.Target {
			return node, nil
		}
		info, err := b.coercionInfo(b.Info(request.Target, node.SourcePosition(), node.Properties()), node)
		if err != nil {
			return nil, err
		}
		if selected, handled, selectionErr := b.declaredCoercion(node, info.Usage, sqltype.Implicit, pos, argument.Raw); handled {
			return selected, selectionErr
		}
		coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), info.Usage, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		info.Property = CoercionProperties(node, coercionPlan)
		return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}, nil
	case functions.ConditionalArgument:
		return b.CoerceConditional(operand, request.Target, request.Label)
	case functions.JSONFormattedArgument, functions.TypeNameCastArgument:
		if err := b.CaptureTypeDiagnosticInput(request.Target.Type); err != nil {
			return nil, err
		}
		if request.Target.Type.ContainsCatalogIdentifier() {
			if err := b.captureElementInput(); err != nil {
				return nil, err
			}
		}
		if request.Plan.RequiresCatalogOutput() {
			b.Prepared.RegclassConcat = true
		}
		node := argument.Expression
		var err error
		if request.Intent == functions.TypeNameCastArgument && argument.Usage.Type == nil {
			node, err = b.finalizeOperand(operand, request.Target.Type)
		} else if request.Intent != functions.TypeNameCastArgument || argument.Usage != request.Target {
			if request.Intent == functions.TypeNameCastArgument && argument.Usage.Type.BaseType() != request.Target.Type.BaseType() {
				node, err = b.CoerceSelected(node, request.Target, sqltype.Explicit, node.SourcePosition(), argument.Raw)
			} else {
				var info expr.Info
				info, err = b.coercionInfo(b.Info(request.Target, node.SourcePosition(), CoercionProperties(node, request.Plan)), node)
				if err == nil {
					node = plan.CoercionExpr{Info: info, Child: node, Plan: request.Plan, Mode: sqltype.Explicit}
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if request.Intent == functions.TypeNameCastArgument {
			b.Prepared.CaptureComposite(PreparedTypeKey{Offset: request.TypeNameCallPosition.Offset, Kind: TypeNameCast}, request.Target, nil)
		}
		if request.Intent == functions.TypeNameCastArgument && (b.Params.NoParameters || b.Consumer.frozenDefinition) {
			// Resolution selected a cast, not an executable routine. Synchronize
			// the frozen recipe with that selection while retaining the written
			// child; later overloads must not reinterpret the call on replay.
			if b.Stored.Syntax == nil {
				b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
			}
			target := request.Target.Type
			schema, err := b.typeNameCallSchema(target)
			if err != nil {
				return nil, err
			}
			frozen := parser.CastExpr{
				Child: argument.Raw,
				TypeName: parser.TypeName{
					Name: target.Name(), Schema: &parser.Identifier{Text: schema}, Pos: request.TypeNameCallPosition,
				},
				Syntax: parser.StandardCast, Pos: request.TypeNameCallPosition,
			}
			if selected, ok := node.(plan.DatumCastExpr); ok {
				frozen.FrozenMethod, frozen.FrozenContext = selected.Method, selected.Context
			}
			b.Stored.Syntax[StoredExpressionSyntaxKey{Frame: b.FrameID(), Position: request.TypeNameCallPosition, Input: argument.Raw.SourcePosition()}] = frozen
		}
		return node, nil
	case functions.BareFormalArgument:
		node := argument.Expression
		if node == nil || !request.Planned {
			return nil, fmt.Errorf("stuntgres: invalid bare function argument coercion")
		}
		properties := CoercionProperties(node, request.Plan)
		if request.PreserveProperties {
			properties = node.Properties()
		}
		return plan.CoercionExpr{Info: b.Info(request.Target, pos, properties), Child: node, Plan: request.Plan, Mode: sqltype.Implicit}, nil
	case functions.AssignmentFormalArgument:
		if !b.Clause.SelectedCallArguments || argument.Expression == nil || argument.Usage.Type == nil {
			return b.Bind(argument.Raw, ExpressionPolicy{Context: AssignmentContext, Expected: request.Target, Mode: sqltype.Implicit})
		}
	case functions.DirectFormalArgument:
		if argument.Usage.Type == nil {
			return b.Bind(argument.Raw, ExpressionPolicy{Context: FunctionArgumentContext, Expected: request.Target, Mode: sqltype.Implicit})
		}
	case functions.ExactFormalArgument, functions.TextFormalArgument, functions.CollatedTextFormalArgument:
		node, err := b.finalizeOperand(operand, request.Target.Type)
		if err != nil {
			return nil, err
		}
		argument.Expression, argument.Usage = node, node.ResultUsage()
		if request.Intent == functions.TextFormalArgument && request.Target.Type.ID() == sqltype.Text && argument.Usage.Collation.ID != 0 {
			request.Target.Collation = argument.Usage.Collation
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid function argument coercion")
	}
	node := argument.Expression
	if argument.Usage == request.Target {
		return node, nil
	}
	if selected, handled, selectionErr := b.declaredCoercion(node, request.Target, sqltype.Implicit, pos, argument.Raw); handled {
		return selected, selectionErr
	}
	coercionPlan := request.Plan
	var err error
	if !request.Planned {
		coercionPlan, err = sqltype.PlanCoercion(argument.Usage, request.Target, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
	}
	properties := node.Properties()
	if !request.PreserveProperties {
		properties = CoercionProperties(node, coercionPlan)
	}
	if !request.AtRaw {
		pos = node.SourcePosition()
	}
	info := b.Info(request.Target, pos, properties)
	switch {
	case request.Intent == functions.CollatedTextFormalArgument:
		info, err = b.CollatedInfo(info, node)
	case request.Intent == functions.TextFormalArgument && request.Target.Type.ID() == sqltype.Text:
		info, err = b.coercionInfo(info, node)
	}
	if err == nil && info.RoutineReference == nil {
		info, err = b.coercionRoutineInfo(info, node)
	}
	if err != nil {
		return nil, err
	}
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}, nil
}

func (b *Binder) comparisonOperandPolicy(raw parser.Expr, policy ExpressionPolicy) (ComparisonOperand, error) {
	if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return ComparisonOperand{raw: raw}, nil
	}
	if annotation, ok := raw.(parser.CastExpr); ok && unknownNullSyntax(raw) {
		var target sqltype.Usage
		var err error
		if policy.thresholdArrayInput {
			target, err = b.Host.ResolveAnnotationTypeInput(annotation.TypeName, true, false, false, true)
		} else {
			target, err = b.Host.ResolveAnnotationType(annotation.TypeName, true)
		}
		if err != nil {
			return ComparisonOperand{}, err
		}
		if target.Type.ID() == sqltype.Unknown {
			return ComparisonOperand{raw: annotation.Child}, nil
		}
	}
	switch raw.(type) {
	case parser.InListExpr, parser.ArrayComparisonExpr:
	case parser.RowExpr, parser.FieldExpr, parser.StarExpr, parser.CurrentDateTimeExpr, parser.IdentityExpr, parser.ColumnExpr, parser.ParameterExpr, parser.LiteralExpr, parser.TypedLiteralExpr, parser.CastExpr, parser.CollateExpr, parser.TimeZoneExpr, parser.OperatorExpr, parser.CallExpr, parser.RegclassExpr, parser.ArrayExpr, parser.SubscriptExpr, parser.FunctionExpr, parser.AggregateExpr, parser.GroupingExpr, parser.CaseExpr, parser.ConditionalExpr, parser.LogicalExpr, parser.PredicateExpr, parser.IndexSubqueryExpr, parser.SubqueryExpr:
	case parser.SQLJSONExpr, parser.SQLJSONQueryExpr, parser.JSONPredicateExpr:
	case parser.MergeActionExpr:
	default:
		return ComparisonOperand{}, fmt.Errorf("stuntgres: invalid comparison operand")
	}
	node, err := b.Bind(raw, policy)
	return ComparisonOperand{Node: node, raw: raw}, err
}

func (b *Binder) CoerceParameter(p plan.ParameterExpr, u sqltype.Usage) (expr.Expr, error) {
	if existing := b.Params.Parameters[p.Number]; existing.Type != nil && existing.Type != u.Type {
		d := expr.ErrorAt("42P08", fmt.Sprintf("inconsistent types deduced for parameter $%d", p.Number), p.Pos).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("%s versus %s", existing.Type.DiagnosticName(), u.Type.DiagnosticName())
		return nil, d
	}
	b.Params.Parameters[p.Number] = u.Type.Usage()
	return plan.ParameterExpr{Info: b.Info(u.Type.Usage(), p.Pos, p.Property), Number: p.Number}, nil
}

func (b *Binder) finalizeOperand(o ComparisonOperand, typ *sqltype.Descriptor) (expr.Expr, error) {
	if o.descriptor() != nil {
		return o.Node, nil
	}
	if p, ok := o.Node.(plan.ParameterExpr); ok {
		return b.CoerceParameter(p, typ.Usage())
	}
	if o.raw == nil {
		return nil, fmt.Errorf("stuntgres: missing unknown comparison operand")
	}
	return b.Bind(o.raw, ExpressionPolicy{Context: ProjectionContext, Expected: typ.Usage()})
}
