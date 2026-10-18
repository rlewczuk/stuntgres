package engine

import (
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
