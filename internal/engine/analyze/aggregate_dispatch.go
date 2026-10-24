package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindRegisteredAggregate binds a call that the functions aggregate registry
// claims. The shared aggregate placement gates have already run.
// It returns the bound node, not yet owned, and the SQL/JSON RETURNING usage
// that the caller applies after ownership (zero when absent).
func (b *Binder) BindRegisteredAggregate(raw parser.AggregateExpr, name parser.ObjectName, definition functions.AggregateDefinition) (expr.Expr, sqltype.Usage, error) {
	if definition.BindReturning != nil {
		return definition.BindReturning(b, raw, name)
	}
	node, err := definition.Bind(b, raw, name)
	return node, sqltype.Usage{}, err
}

// aggregateReturning applies a SQL/JSON aggregate's RETURNING clause to the
// owned aggregate as an assignment coercion.
func (b *Binder) aggregateReturning(raw parser.AggregateExpr, node expr.Expr, returning sqltype.Usage) (expr.Expr, error) {
	if err := b.CaptureTypeDiagnosticInput(returning.Type); err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.Info(returning, raw.Pos, node.Properties()), node)
	if err != nil {
		return nil, err
	}
	coercionPlan, err := functions.JSONReturningCoercion(node.ResultUsage(), info.Usage, raw.JSON.Output)
	if err != nil {
		return nil, err
	}
	info.Property = CoercionProperties(node, coercionPlan)
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Assignment}, nil
}

// rejectOrdinaryWithin analyzes WITHIN GROUP on an aggregate that is not an
// ordered-set aggregate as an ordinary call first, so its errors win.
func (b *Binder) rejectOrdinaryWithin(raw parser.AggregateExpr) (expr.Expr, error) {
	combined := raw
	combined.Args = make([]parser.Expr, 0, len(raw.Args)+len(raw.Order))
	combined.Args = append(combined.Args, raw.Args...)
	for _, key := range raw.Order {
		combined.Args = append(combined.Args, key.Expr)
	}
	combined.Within, combined.Order, combined.Window = false, nil, nil
	if _, err := b.Bind(combined, ExpressionPolicy{Context: ProjectionContext}); err != nil {
		return nil, err
	}
	return nil, expr.ErrorAt("42809", raw.Name+" is not an ordered-set aggregate, so it cannot have WITHIN GROUP", raw.Pos)
}
