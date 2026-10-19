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
	// BindReturning replaces Bind for the families with SQL/JSON syntax. It
	// also returns the RETURNING usage (zero when absent), which the engine
	// applies as an assignment coercion after its aggregate ownership checks.
	BindReturning func(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error)
	// Window resolves the signature of a window call of the aggregate with
	// the same analysis, without building a provider or registering
	// aggregate state. It returns the coerced argument and the result usage.
	// The WITHIN GROUP aggregates have none (see their entry).
	Window func(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error)
	// ProbeCall performs pure pseudo-signature applicability before a winner
	// is handed back to this family's normal aggregate binder.
	ProbeCall CallShape
}

// The static table is the registry: no init() registration. Names must not
// overlap any aggregate the engine still dispatches itself.
var aggregateDefinitions = [...]AggregateDefinition{
	{Names: []string{"count"}, Bind: BindCount, Window: windowCount, ProbeCall: probeAnyAggregate},
	{Names: []string{"sum"}, ValuesNamespace: true, Bind: bindSum, Window: windowSum},
	{Names: []string{"avg"}, Bind: bindAvg, Window: windowAvg},
	{Names: []string{"min", "max"}, Bind: bindExtrema, Window: windowExtrema},
	{Names: []string{"any_value"}, Bind: bindAnyValue, Window: windowAnyValue, ProbeCall: probeAnyAggregate},
	{Names: []string{"bool_and", "bool_or", "every"}, Bind: bindBooleanAggregate, Window: windowBooleanAggregate},
	{Names: []string{"bit_and", "bit_or", "bit_xor"}, ValuesNamespace: true, Bind: bindBitwiseAggregate, Window: windowBitwiseAggregate},
	{Names: statisticalNames[:], Bind: bindStatisticalAggregate, Window: windowStatisticalAggregate},
	{Names: []string{"string_agg"}, Bind: bindStringAggregate, Window: windowStringAggregate},
	{Names: []string{"array_agg"}, Bind: bindArrayAggregate, Window: windowArrayAggregate, ProbeCall: probeArrayAggregate},
	{Names: []string{"range_agg", "range_intersect_agg"}, Bind: bindRangeAggregate, Window: windowRangeAggregate, ProbeCall: probeRangeAggregate},
	{Names: jsonArrayAggregateNames[:], BindReturning: bindJSONArrayAggregate, Window: windowJSONArrayAggregate, ProbeCall: probeAnyAggregate},
	{Names: jsonObjectAggregateNames[:], BindReturning: bindJSONObjectAggregate, Window: windowJSONObjectAggregate, ProbeCall: probeAnyAggregate},
	{Names: []string{"json_objectagg"}, BindReturning: bindJSONObjectAggregate, Window: windowSQLJSONObjectAggregate},
	// WITHIN GROUP aggregates reject OVER while binding; the rank family's
	// window calls bind as window functions.
	{Names: []string{"percentile_cont", "percentile_disc", "mode", "rank", "dense_rank", "percent_rank", "cume_dist"}, Bind: bindOrderedSet, ProbeCall: probeAnyAggregate},
}

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
