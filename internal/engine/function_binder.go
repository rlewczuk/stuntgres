package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The expression binder is the analyzer that builtin bind code reaches
// through functions.Binder. These methods map the interface onto the
// binder's existing private services; they add no state of their own.
var _ functions.Binder = (*expressionBinder)(nil)

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

// InstalledRoutine resolves the namespace path first, so its lookup errors
// precede every signature diagnostic; the procedure lookup itself is pure.
func (b *expressionBinder) InstalledRoutine(name parser.ObjectName, extension functions.Extension, signature string) (functions.InstalledRoutine, error) {
	auth, err := b.bindingAuthorization()
	if err != nil {
		return nil, err
	}
	path, err := b.engine.lookupNamespacesSnapshot(auth, name, installedFunctionLookup)
	if err != nil {
		return nil, err
	}
	proc := procedureInNamespaces(auth.catalog, path, name.Parts[len(name.Parts)-1].Text, procedureSignature(signature))
	switch extension {
	case functions.UUIDOsspExtension:
		if proc == nil || proc.uuidOssp != stockUUIDOssp {
			return nil, nil
		}
		return installedUUIDOsspRoutine{binder: b, catalog: auth.catalog, procedure: proc}, nil
	}
	return nil, fmt.Errorf("stuntgres: unknown installed extension %d", extension)
}

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
