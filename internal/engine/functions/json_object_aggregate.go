package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"

	"stuntgres/internal/sqltype"
)

// JSONObjectAggregate is json[b]_object_agg[_unique][_strict] and
// JSON_OBJECTAGG. Its key and value input usages are known only after ORDER
// BY and DISTINCT matching, so the engine completes it in FinishAggregate.
type JSONObjectAggregate struct {
	key, input, usage sqltype.Usage
	strict, unique    bool
	plan              sqltype.JSONObjectAggregatePlan
}

// NewJSONObjectAggregate returns the provider before its inputs complete it.
func NewJSONObjectAggregate(usage sqltype.Usage, strict, unique bool) JSONObjectAggregate {
	return JSONObjectAggregate{usage: usage, strict: strict, unique: unique}
}

func (p JSONObjectAggregate) ResultUsage() sqltype.Usage { return p.usage }
func (p JSONObjectAggregate) KeyUsage() sqltype.Usage    { return p.key }
func (p JSONObjectAggregate) InputUsage() sqltype.Usage  { return p.input }
func (p JSONObjectAggregate) Strict() bool               { return p.strict }
func (p JSONObjectAggregate) Unique() bool               { return p.unique }

func (p JSONObjectAggregate) Init(ctx context.Context) (any, error) {
	return p.InitWithEnvironment(ctx, sqltype.OutputEnvironment{})
}
func (p JSONObjectAggregate) InitWithEnvironment(ctx context.Context, env sqltype.OutputEnvironment) (any, error) {
	return p.plan.Init(ctx, env)
}
func (p JSONObjectAggregate) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	key := values[0]
	value := values[1]
	s, ok := state.(*sqltype.JSONObjectAggregateState)
	if !ok {
		return fmt.Errorf("stuntgres: invalid JSON object aggregate state")
	}
	return p.plan.Advance(ctx, s, key, value)
}
func (p JSONObjectAggregate) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	s, ok := state.(*sqltype.JSONObjectAggregateState)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid JSON object aggregate state")
	}
	return p.plan.Finalize(ctx, s)
}

// Complete binds the provider to its finished key and value input usages.
// Unresolved occurrences are never executable; the engine diagnoses them
// after the whole query has supplied its occurrence-sensitive constraints.
func (p JSONObjectAggregate) Complete(key, input sqltype.Usage) (JSONObjectAggregate, error) {
	p.key, p.input = key, input
	if key.Type == nil || input.Type == nil {
		return p, nil
	}
	var err error
	p.plan, err = sqltype.BindJSONObjectAggregate(key, input, p.usage, p.strict, p.unique)
	return p, err
}

// Reconstruct rebinds the provider for remapped key, value and result usages.
func (p JSONObjectAggregate) Reconstruct(key, input, usage sqltype.Usage) (JSONObjectAggregate, error) {
	p.key, p.input, p.usage = key, input, usage
	var err error
	p.plan, err = sqltype.BindJSONObjectAggregate(p.key, p.input, p.usage, p.strict, p.unique)
	return p, err
}
