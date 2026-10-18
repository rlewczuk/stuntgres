package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/sqltype"
)

// Immutable provider; all tuple storage and sort indices belong to execution.
// The hypothetical row is compared, never inserted into shared ordered inputs.
type hypotheticalSetProvider struct {
	usage     sqltype.Usage
	operation functions.AggregateOperation
}

func (p *hypotheticalSetProvider) ResultUsage() sqltype.Usage            { return p.usage }
func (p *hypotheticalSetProvider) Init(ctx context.Context) (any, error) { return nil, ctx.Err() }
func (p *hypotheticalSetProvider) Advance(context.Context, any, sqltype.Value, sqltype.Value) error {
	return fmt.Errorf("stuntgres: hypothetical input requires borrowed tuples")
}
func (p *hypotheticalSetProvider) Finalize(context.Context, any) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: hypothetical aggregate requires ordered finalization")
}
func (p *hypotheticalSetProvider) PrepareOrdered(ctx context.Context, _ []sqltype.Value, _ int) (orderedSetPreparation, error) {
	return orderedSetPreparation{}, ctx.Err()
}
func (p *hypotheticalSetProvider) FinalizeOrdered(ctx context.Context, _ orderedSetPreparation, in orderedAggregateInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	var before, equal, distinct int64
	for i := range in.len() {
		row := in.tuple(i)
		sign, err := compareOrderedAggregateTuple(ctx, in.order, row, in.target, env)
		if err != nil {
			return sqltype.Value{}, err
		}
		if sign > 0 {
			break
		}
		if sign == 0 {
			if p.operation != functions.AggregateHypotheticalCumeDist {
				break
			}
			equal++
			continue
		}
		before++
		if p.operation == functions.AggregateHypotheticalDenseRank {
			if i == 0 {
				distinct++
			} else {
				peer, err := compareOrderedAggregateTuple(ctx, in.order, in.tuple(i-1), row, env)
				if err != nil {
					return sqltype.Value{}, err
				}
				if peer != 0 {
					distinct++
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	switch p.operation {
	case functions.AggregateHypotheticalRank:
		return sqltype.Integer(p.usage.Type, before+1)
	case functions.AggregateHypotheticalDenseRank:
		return sqltype.Integer(p.usage.Type, distinct+1)
	case functions.AggregateHypotheticalPercentRank:
		value := float64(0)
		if in.len() > 0 {
			value = float64(before) / float64(in.len())
		}
		return sqltype.Float(p.usage.Type, value)
	case functions.AggregateHypotheticalCumeDist:
		return sqltype.Float(p.usage.Type, float64(before+equal+1)/float64(in.len()+1))
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid hypothetical aggregate operation")
}
