package engine

import (
	"context"

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

func (b *expressionBinder) DefaultConsumer() bool { return b.defaultConsumer }

func (b *expressionBinder) ArrayType(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return b.definitionArray(element)
}

// The probe passes no label, so selectCommonUsage never fails; operands only
// contribute their usages.
func (b *expressionBinder) CommonUsage(usages []sqltype.Usage) sqltype.Usage {
	operand := func(i int) comparisonOperand {
		return comparisonOperand{node: literalExpression{expressionInfo: expressionInfo{Usage: usages[i]}}}
	}
	common, _ := selectCommonUsage(len(usages), operand, b.typ(sqltype.Text).Usage(), "", b.diagnosticTypeName)
	return common
}

func (b *expressionBinder) ComparisonInfo(left, right expr.Expr) (expr.Info, error) {
	return b.comparisonInfo(left, right)
}

func (b *expressionBinder) Literal(value sqltype.Value, pos parser.Position) expr.Expr {
	return literalExpression{b.info(value.Type().Usage(), pos, ExprProperties{Volatility: Immutable}), value}
}

func (b *expressionBinder) Context() context.Context { return b.ctx }

func (b *expressionBinder) AdmitSetReturning(pos parser.Position) error {
	if err := b.stringSetAllowed(pos); err != nil {
		return err
	}
	switch b.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.queryHasTargetSet = true
	}
	return nil
}

func (b *expressionBinder) CollatedInfo(info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
	return b.collatedInfo(info, inputs...)
}

func (b *expressionBinder) RetainUntypedParameter(arg expr.Expr) {
	if parameter, ok := arg.(parameterExpression); ok && parameter.Usage.Type == nil {
		b.anyParameters = append(b.anyParameters, parameter)
	}
}

func (b *expressionBinder) NoteCatalogText() { b.regclassConcat = true }

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

func (b *expressionBinder) NoteVisibilitySnapshot() { b.visibilitySnapshot = true }

func (b *expressionBinder) Frame() uint32 { return b.frameID() }

func (b *expressionBinder) CapturePolymorphicBody(node expr.Expr) error {
	return b.capturePolymorphicBody(node)
}

func (b *expressionBinder) TargetSet() {
	switch b.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.queryHasTargetSet = true
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
	}, filter, info)
	if err != nil || !call.RetainUntypedArgument {
		return result, err
	}
	// ORDER BY matching may have resolved an UNKNOWN target; only an actual
	// remaining ANY occurrence joins the unresolved-parameter guard.
	b.RetainUntypedParameter(b.aggregates[result.(aggregateExpression).slot].argument)
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
	d := auth.catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	return typeDiagnosticNameAt(auth.catalog, d, path), nil
}
