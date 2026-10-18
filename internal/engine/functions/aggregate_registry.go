package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// AggregateDefinition registers one aggregate family with the engine's
// aggregate dispatch, which runs after the shared aggregate placement gates
// (recursive terms, EXECUTE parameters, generated, index, CHECK and VALUES
// consumers). Names are matched exactly: the parser folds aggregate names.
type AggregateDefinition struct {
	Names []string
	// ValuesNamespace asks the engine to bind the call, in INSERT ... VALUES
	// without a source namespace, with the insert target as an inaccessible
	// diagnostic candidate rather than an input.
	ValuesNamespace bool
	// Bind analyzes the arguments and FILTER, resolves the signature, selects
	// the provider and finishes the call through Binder.FinishAggregate.
	Bind func(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error)
	// Window resolves the signature of a window call of the aggregate with
	// the same analysis, without building a provider or registering
	// aggregate state. It returns the coerced argument and the result usage.
	Window func(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error)
}

// The static table is the registry: no init() registration. Names must not
// overlap any aggregate the engine still dispatches itself.
var aggregateDefinitions = [...]AggregateDefinition{}

// LookupAggregate returns the definition that claims an aggregate name.
func LookupAggregate(name string) (AggregateDefinition, bool) {
	for _, definition := range aggregateDefinitions {
		for _, candidate := range definition.Names {
			if candidate == name {
				return definition, true
			}
		}
	}
	return AggregateDefinition{}, false
}
