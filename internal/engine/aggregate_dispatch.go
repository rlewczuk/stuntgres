package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindRegisteredAggregate binds a call that the functions aggregate registry
// claims. The shared aggregate placement gates have already run.
// It returns the bound node, not yet owned, and the SQL/JSON RETURNING usage
// that the caller applies after ownership (zero when absent).
func (b *expressionBinder) bindRegisteredAggregate(raw parser.AggregateExpr, name parser.ObjectName, definition functions.AggregateDefinition) (BoundExpr, sqltype.Usage, error) {
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
func (b *expressionBinder) aggregateReturning(raw parser.AggregateExpr, node BoundExpr, returning sqltype.Usage) (BoundExpr, error) {
	if err := b.captureTypeDiagnosticInput(returning.Type); err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.info(returning, raw.Pos, node.Properties()), node)
	if err != nil {
		return nil, err
	}
	plan, err := functions.JSONReturningCoercion(node.ResultUsage(), info.Usage, raw.JSON.Output)
	if err != nil {
		return nil, err
	}
	info.Property = coercionProperties(node, plan)
	return coercionExpression{info, node, plan, sqltype.Assignment, false}, nil
}

// valuesAggregateNamespace gives a call bound in INSERT ... VALUES, which has
// no source namespace, an empty one in which the insert target is only an
// inaccessible diagnostic candidate: it is never added to the source
// namespace or read manifest. The returned function restores the binder.
func (b *expressionBinder) valuesAggregateNamespace() func() {
	if b.aggregateClause != "INSERT VALUES" || b.queryNamespace != nil {
		return func() {}
	}
	b.queryNamespace = &queryNamespace{}
	hidden := b.queryHidden
	if target := b.insertTarget; target != nil {
		columns := make([]queryColumn, len(target.columns))
		for i, descriptor := range target.columns {
			columns[i].descriptor = descriptor
		}
		b.queryHidden = append(b.queryHidden, queryQualifier{name: target.name, columns: columns, lateralForbidden: true})
	}
	return func() { b.queryNamespace, b.queryHidden = nil, hidden }
}
