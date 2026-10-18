package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

// bindRegisteredAggregate binds a call that the functions aggregate registry
// claims. The shared aggregate placement gates have already run.
func (b *expressionBinder) bindRegisteredAggregate(raw parser.AggregateExpr, name parser.ObjectName, definition functions.AggregateDefinition) (BoundExpr, error) {
	if definition.ValuesNamespace {
		defer b.valuesAggregateNamespace()()
	}
	return definition.Bind(b, raw, name)
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
