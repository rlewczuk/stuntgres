package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BindRoutineDefault analyzes a declaration recipe without formals, external
// parameters or producer execution. Unlike table defaults, arbitrary supported
// volatile scalar functions are allowed.
func (b *Binder) BindRoutineDefault(raw parser.Expr, usage sqltype.Usage) (expr.Expr, error) {
	previous := b.Consumer
	b.Consumer.DefaultConsumer, b.Consumer.RoutineDefault = true, true
	defer func() { b.Consumer = previous }()
	node, err := b.Bind(raw, ExpressionPolicy{Context: ProjectionContext, Expected: usage.Type.Usage()})
	if err != nil {
		return nil, err
	}
	if node.ResultUsage() != usage {
		input, annotations := DomainCoercionInput(node, usage)
		info, failure := StorageCoercionInfo(b.BindingCatalog(), b.Info(usage, parser.ExpressionStart(raw), input.Properties()), input)
		if failure != nil {
			return nil, failure
		}
		coercion, failure := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if failure != nil {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of DEFAULT must be type %s, not type %s", usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName()), parser.ExpressionStart(raw))
		}
		info.Property = CoercionProperties(input, coercion)
		node = RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercion, Mode: sqltype.Assignment})
	}
	return StorageDestination(node, usage, parser.ExpressionStart(raw))
}
