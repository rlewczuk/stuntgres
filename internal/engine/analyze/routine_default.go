package analyze

import (
	"stuntgres/internal/engine/expr"
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
	if sqltype.PolymorphicFamily(usage.Type.ID()) != 0 {
		return b.Bind(raw, ExpressionPolicy{Context: ProjectionContext})
	}
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
		converted, failure := b.coerceAssignmentForConstruct(input, info.Usage, "DEFAULT", raw)
		if failure != nil {
			return nil, failure
		}
		node = RestoreDomainCastCollations(annotations, converted)
	}
	return StorageDestination(node, usage, parser.ExpressionStart(raw))
}
