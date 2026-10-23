package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"

	"stuntgres/internal/sqltype"
)

// Immutable provider; all tuple storage and sort indices belong to execution.
// The hypothetical row is compared, never inserted into shared ordered inputs.
type HypotheticalSet struct {
	usage     sqltype.Usage
	operation AggregateOperation
}

func (p *HypotheticalSet) ResultUsage() sqltype.Usage            { return p.usage }
func (p *HypotheticalSet) Init(ctx context.Context) (any, error) { return nil, ctx.Err() }
func (p *HypotheticalSet) Advance(context.Context, any, []sqltype.Value, expr.Context) error {
	return fmt.Errorf("stuntgres: hypothetical input requires borrowed tuples")
}
func (p *HypotheticalSet) Finalize(context.Context, any) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: hypothetical aggregate requires ordered finalization")
}
func (p *HypotheticalSet) PrepareOrdered(ctx context.Context, _ []sqltype.Value, _ int) (OrderedPreparation, error) {
	return OrderedPreparation{}, ctx.Err()
}
func (p *HypotheticalSet) FinalizeOrdered(ctx context.Context, _ OrderedPreparation, in OrderedInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	var before, equal, distinct int64
	for i := range in.Len() {
		row := in.Tuple(i)
		sign, err := in.Compare(ctx, row, in.Target, env)
		if err != nil {
			return sqltype.Value{}, err
		}
		if sign > 0 {
			break
		}
		if sign == 0 {
			if p.operation != AggregateHypotheticalCumeDist {
				break
			}
			equal++
			continue
		}
		before++
		if p.operation == AggregateHypotheticalDenseRank {
			if i == 0 {
				distinct++
			} else {
				peer, err := in.Compare(ctx, in.Tuple(i-1), row, env)
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
	case AggregateHypotheticalRank:
		return sqltype.Integer(p.usage.Type, before+1)
	case AggregateHypotheticalDenseRank:
		return sqltype.Integer(p.usage.Type, distinct+1)
	case AggregateHypotheticalPercentRank:
		value := float64(0)
		if in.Len() > 0 {
			value = float64(before) / float64(in.Len())
		}
		return sqltype.Float(p.usage.Type, value)
	case AggregateHypotheticalCumeDist:
		return sqltype.Float(p.usage.Type, float64(before+equal+1)/float64(in.Len()+1))
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid hypothetical aggregate operation")
}

// NewHypotheticalSet builds rank, dense_rank, percent_rank or cume_dist as a
// hypothetical-set aggregate.
func NewHypotheticalSet(usage sqltype.Usage, operation AggregateOperation) *HypotheticalSet {
	return &HypotheticalSet{usage: usage, operation: operation}
}

func (p *HypotheticalSet) Operation() AggregateOperation { return p.operation }
