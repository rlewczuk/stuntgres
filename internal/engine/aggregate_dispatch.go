package engine

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
func (b *expressionBinder) bindRegisteredAggregate(raw parser.AggregateExpr, name parser.ObjectName, definition functions.AggregateDefinition) (expr.Expr, sqltype.Usage, error) {
	if definition.ValuesNamespace {
		defer b.valuesAggregateNamespace()()
	}
	if definition.BindReturning != nil {
		return definition.BindReturning(b, raw, name)
	}
	node, err := definition.Bind(b, raw, name)
	return node, sqltype.Usage{}, err
}

// aggregateReturning applies a SQL/JSON aggregate's RETURNING clause to the
// owned aggregate as an assignment coercion.
func (b *expressionBinder) aggregateReturning(raw parser.AggregateExpr, node expr.Expr, returning sqltype.Usage) (expr.Expr, error) {
	if err := b.captureTypeDiagnosticInput(returning.Type); err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.info(returning, raw.Pos, node.Properties()), node)
	if err != nil {
		return nil, err
	}
	coercionPlan, err := functions.JSONReturningCoercion(node.ResultUsage(), info.Usage, raw.JSON.Output)
	if err != nil {
		return nil, err
	}
	info.Property = coercionProperties(node, coercionPlan)
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Assignment}, nil
}

// valuesAggregateNamespace gives a call bound in INSERT ... VALUES, which has
// no source namespace, an empty one in which the insert target is only an
// inaccessible diagnostic candidate: it is never added to the source
// namespace or read manifest. The returned function restores the binder.
func (b *expressionBinder) valuesAggregateNamespace() func() {
	if b.clause.aggregateClause != "INSERT VALUES" || b.query.namespace != nil {
		return func() {}
	}
	b.query.namespace = &queryNamespace{}
	hidden := b.query.hidden
	if target := b.target.insertTarget; target != nil {
		columns := make([]queryColumn, len(target.Columns))
		for i, descriptor := range target.Columns {
			columns[i].descriptor = descriptor
		}
		b.query.hidden = append(b.query.hidden, queryQualifier{name: target.Name(), columns: columns, lateralForbidden: true})
	}
	return func() { b.query.namespace, b.query.hidden = nil, hidden }
}

// rejectOrdinaryWithin analyzes WITHIN GROUP on an aggregate that is not an
// ordered-set aggregate as an ordinary call first, so its errors win.
func (b *expressionBinder) rejectOrdinaryWithin(raw parser.AggregateExpr) (expr.Expr, error) {
	combined := raw
	combined.Args = make([]parser.Expr, 0, len(raw.Args)+len(raw.Order))
	combined.Args = append(combined.Args, raw.Args...)
	for _, key := range raw.Order {
		combined.Args = append(combined.Args, key.Expr)
	}
	combined.Within, combined.Order, combined.Window = false, nil, nil
	if _, err := b.bind(combined, expressionPolicy{context: projectionContext}); err != nil {
		return nil, err
	}
	return nil, expr.ErrorAt("42809", raw.Name+" is not an ordered-set aggregate, so it cannot have WITHIN GROUP", raw.Pos)
}
