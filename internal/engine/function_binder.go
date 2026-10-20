package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The expression binder is the analyzer that builtin bind code reaches
// through functions.Binder. These methods map the interface onto the
// binder's existing private services; they add no state of their own.
var _ functions.AggregateBinder = (*expressionBinder)(nil)

func (b *expressionBinder) Type(id sqltype.ID) *sqltype.Descriptor { return b.typ(id) }

// OriginalArgument recovers syntax only through the same private slot that
// supplies an already-analyzed operand. It never binds or matches source keys.
func (b *expressionBinder) OriginalArgument(raw parser.Expr) (parser.Expr, bool) {
	if slot, ok := raw.(parser.ParameterExpr); ok && slot.Number < 0 && -slot.Number <= len(b.clause.selectedOperatorSyntax) {
		return b.clause.selectedOperatorSyntax[-slot.Number-1], true
	}
	return raw, false
}

func (b *expressionBinder) Info(u sqltype.Usage, pos parser.Position, p expr.Properties) expr.Info {
	return b.info(u, pos, p)
}

func (b *expressionBinder) BuiltinVisible(name parser.ObjectName) (bool, error) {
	return b.builtinVisible(name, functionLookup)
}

func (b *expressionBinder) BuiltinRoutineInfo(info expr.Info, provider functions.RoutineProvider, name string, inputs ...sqltype.ID) (expr.Info, error) {
	return b.builtinRoutineInfo(info, provider, name, inputs...)
}

func (b *expressionBinder) BuiltinRoutineReference(provider functions.RoutineProvider, name string, inputs ...sqltype.ID) (ids.ObjectRef, error) {
	return b.builtinRoutineReference(provider, name, inputs...)
}

func (b *expressionBinder) DiagnosticTypeName(typ *sqltype.Descriptor) string {
	return b.diagnosticTypeName(typ)
}

func (b *expressionBinder) SignatureError(name parser.ObjectName, pos parser.Position, types []string) error {
	return b.functionSignatureErrorForTypes(name, pos, types)
}

func (b *expressionBinder) AnalyzeFilter(filter parser.Expr) error {
	_, err := b.bindAggregateFilter(parser.AggregateExpr{Filter: filter})
	return err
}

func (b *expressionBinder) DefaultConsumer() bool { return b.consumer.defaultConsumer }

func (b *expressionBinder) ArrayType(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return b.definitionArray(element)
}

// The probe passes no label, so selectCommonUsage never fails; operands only
// contribute their usages.
func (b *expressionBinder) CommonUsage(usages []sqltype.Usage) sqltype.Usage {
	operand := func(i int) comparisonOperand {
		return comparisonOperand{node: literalExpression{Info: expr.Info{Usage: usages[i]}}}
	}
	common, _ := selectCommonUsage(len(usages), operand, b.typ(sqltype.Text).Usage(), "", b.diagnosticTypeName)
	return common
}

func (b *expressionBinder) ComparisonInfo(left, right expr.Expr) (expr.Info, error) {
	return b.comparisonInfo(left, right)
}

func (b *expressionBinder) Literal(value sqltype.Value, pos parser.Position) expr.Expr {
	return literalExpression{b.info(value.Type().Usage(), pos, expr.Properties{Volatility: expr.Immutable}), value}
}

func (b *expressionBinder) Context() context.Context { return b.ctx }

func (b *expressionBinder) AdmitSetReturning(pos parser.Position) error {
	if err := b.stringSetAllowed(pos); err != nil {
		return err
	}
	switch b.clause.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.clause.queryHasTargetSet = true
	}
	return nil
}

func (b *expressionBinder) CollatedInfo(info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
	return b.collatedInfo(info, inputs...)
}

func (b *expressionBinder) RetainUntypedParameter(arg expr.Expr) bool {
	parameter, ok := arg.(parameterExpression)
	if !ok || parameter.Usage.Type != nil {
		return false
	}
	b.params.anyParameters = append(b.params.anyParameters, parameter)
	return true
}

func (b *expressionBinder) NoteCatalogText()     { b.prepared.regclassConcat = true }
func (b *expressionBinder) NoteCatalogSnapshot() { b.consumer.catalogSnapshot = true }

func (b *expressionBinder) BuiltinOperatorRoutineInfo(info expr.Info, operator string, left, right sqltype.ID) (expr.Info, error) {
	return b.builtinOperatorRoutineInfo(info, operator, left, right)
}

func (b *expressionBinder) CaptureComparisonInput(typ *sqltype.Descriptor) error {
	return b.captureComparisonDiagnosticInput(typ)
}

func (b *expressionBinder) CaptureTypeInput(typ *sqltype.Descriptor) error {
	if err := b.captureTypeDiagnosticInput(typ); err != nil {
		return err
	}
	if typ.ContainsCatalogIdentifier() {
		return b.captureElementInput()
	}
	return nil
}

func (b *expressionBinder) ConstantValue(node expr.Expr) (sqltype.Value, bool) {
	literal, ok := node.(literalExpression)
	return literal.value, ok
}

func (b *expressionBinder) BuiltinOperatorVisible(name parser.ObjectName) (bool, error) {
	return b.builtinVisible(name, operatorLookup)
}

func (b *expressionBinder) CallSignatureError(raw parser.FunctionExpr) error {
	return b.functionSignatureError(raw)
}

func (b *expressionBinder) LiteralNull(arg expr.Expr) bool { return randomLiteralNull(arg) }

func (b *expressionBinder) NoteVisibilitySnapshot() { b.consumer.visibilitySnapshot = true }

func (b *expressionBinder) Frame() uint32 { return b.frameID() }

func (b *expressionBinder) CapturePolymorphicBody(node expr.Expr) error {
	return b.capturePolymorphicBody(node)
}

func (b *expressionBinder) TargetSet() {
	switch b.clause.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.clause.queryHasTargetSet = true
	}
}

func (b *expressionBinder) RegexpSetPlacement(call functions.RegexpCall) error {
	return b.regexpPlacement(call)
}

func (b *expressionBinder) AnalyzeAggregateFilter(raw parser.AggregateExpr) (expr.Expr, error) {
	return b.bindAggregateFilter(raw)
}

func (b *expressionBinder) FinishAggregate(raw parser.AggregateExpr, call functions.AggregateCall, filter expr.Expr, info expr.Info) (expr.Expr, error) {
	result, err := b.finishAggregate(raw, aggregateCall{
		provider:     call.Provider,
		routine:      call.Routine,
		argument:     call.Argument,
		separator:    call.Separator,
		direct:       call.Direct,
		stageWidth:   call.StageWidth,
		operation:    call.Operation,
		includeNulls: call.IncludeNulls,
		order:        withinGroupOrder(call.Order),
	}, filter, info)
	if err != nil || !call.RetainUntypedArgument {
		return result, err
	}
	// ORDER BY matching may have resolved an UNKNOWN target; only an actual
	// remaining ANY occurrence joins the unresolved-parameter guard.
	b.RetainUntypedParameter(b.grouping.aggregates[result.(aggregateExpression).slot].argument)
	return result, nil
}

func (b *expressionBinder) DomainTypeName(typ *sqltype.Descriptor) (string, error) {
	return b.domainTypeName(typ)
}

// extremaArgumentTypeName spells an aggregate operand as DomainTypeName does.
func (b *expressionBinder) extremaArgumentTypeName(operand comparisonOperand) (string, error) {
	return b.domainTypeName(operand.descriptor())
}

func (b *expressionBinder) domainTypeName(typ *sqltype.Descriptor) (string, error) {
	if typ == nil {
		return "unknown", nil
	}
	if !typ.IsDomain() {
		return typ.DiagnosticName(), nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return "", err
	}
	path, err := b.engine.lookupPath(auth, typeLookup)
	if err != nil {
		return "", err
	}
	d := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	return catalog.TypeDiagnosticNameAt(auth.Catalog, d, path), nil
}

func (b *expressionBinder) JSONReturningType(raw parser.TypeName) (sqltype.Usage, error) {
	return b.engine.resolveAnnotationTypeInput(raw, b.op, false, false, true, true, false)
}

// SequenceArgument shares regclass input, with its prepared sequence-literal
// capture, with explicit regclass casts. A column DEFAULT admits only a
// non-NULL captured literal provider.
func (b *expressionBinder) SequenceArgument(raw parser.Expr) (expr.Expr, ids.ObjectRef, error) {
	node, ref, err := b.bindRegclass(raw)
	if err == nil && b.consumer.defaultConsumer {
		literal, ok := node.(literalExpression)
		if !ok || literal.value.IsNull() || ref == (ids.ObjectRef{}) {
			return nil, ref, expr.ErrorAt("0A000", "unsupported sequence DEFAULT argument", parser.ExpressionStart(raw))
		}
	}
	return node, ref, err
}

func (b *expressionBinder) SequenceTemporary(ref ids.ObjectRef, pos parser.Position) (ids.SessionToken, error) {
	var temporary ids.SessionToken
	for _, constant := range b.prepared.sequenceConstants {
		if constant.ref == ref {
			temporary = constant.temporary
		}
	}
	if b.consumer.defaultConsumer {
		if err := validateDefaultTemporaryProvider(temporary, b.consumer.defaultTemporary, pos); err != nil {
			return temporary, err
		}
		// Stored defaults do not enter call-site prepared literal reanalysis.
		b.prepared.sequenceConstants = nil
	}
	return temporary, nil
}

// withinGroupOrder converts a family's WITHIN GROUP keys: an argument key
// reads stage slot 0; other keys own the stage slots after value and
// separator, in key order.
func withinGroupOrder(keys []functions.AggregateSortKey) []sortKey {
	if keys == nil {
		return nil
	}
	order := make([]sortKey, len(keys))
	for i, key := range keys {
		slot, cache := -1, 2+i
		if key.Argument {
			slot, cache = 0, 0
		}
		order[i] = sortKey{expression: key.Expression, comparison: key.Comparison, position: key.Key.Pos.Character, desc: key.Key.Desc, nullsFirst: key.Key.Nulls == parser.NullsFirst || key.Key.Nulls == parser.NullsDefault && key.Key.Desc, slot: slot, cache: cache}
	}
	return order
}

func (b *expressionBinder) CollationName(id uint32) string { return b.collationName(id) }

func (b *expressionBinder) CommonArgumentUsage(operands []functions.Argument, label string) (sqltype.Usage, error) {
	return selectCommonUsage(len(operands), func(i int) comparisonOperand {
		return comparisonOperand{node: operands[i].Expression, raw: operands[i].Raw}
	}, b.typ(sqltype.Text).Usage(), label, (*sqltype.Descriptor).DiagnosticName)
}

func (b *expressionBinder) CollationRelabel(child expr.Expr, info expr.Info) expr.Expr {
	return collationExpression{Info: info, child: child}
}
