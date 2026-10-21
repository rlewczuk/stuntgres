package functions

import (
	"fmt"

	"stuntgres/internal/sqltype"
)

// ProviderRemap is the environment a stored aggregate provider is rebuilt in
// when its view or stored query moves to new catalog generations.
type ProviderRemap struct {
	// Type maps a descriptor to its remapped generation.
	Type func(*sqltype.Descriptor) *sqltype.Descriptor
	// Usage maps a usage's type and collation; it fails when the collation
	// cannot be remapped.
	Usage func(sqltype.Usage) (sqltype.Usage, error)
	// Argument is the call's stored argument usage, before remapping. Ordered
	// sets bind their ordering from it.
	Argument sqltype.Usage
}

// RemappableProvider is an aggregate provider that may persist in a stored
// query. Remap rebuilds it over remapped usages and rebinds the comparison,
// operator and conversion plans it derived from them. It returns the first
// remap or binding error and no provider. Providers without the hook are
// unpersistable.
type RemappableProvider interface {
	AggregateProvider
	Remap(ProviderRemap) (AggregateProvider, error)
}

func (p IntegralSum) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(r.Type(p.InputType()), usage), nil
}

func (p NumericSum) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(r.Type(p.InputType()), usage), nil
}

func (p FloatSum) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(r.Type(p.InputType()), usage), nil
}

func (p MoneySum) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	rebound, err := p.Reconstruct(usage)
	if err != nil {
		return nil, err
	}
	return rebound, nil
}

func (p IntervalSum) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(usage), nil
}

func (p IntervalAvg) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(usage), nil
}

func (p NumericAvg) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(r.Type(p.InputType()), usage), nil
}

func (p FloatAvg) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(r.Type(p.InputType()), usage), nil
}

func (p StatisticalAggregate) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(r.Type(p.InputType()), usage), nil
}

func (p RegressionAggregate) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(r.Type(p.InputType()), usage), nil
}

func (p RegressionCountAggregate) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(r.Type(p.InputType()), usage), nil
}

func (p CountProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return NewCountProvider(usage), nil
}

func (p BooleanAggregateProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return NewBooleanAggregateProvider(usage, p.Conjunction()), nil
}

func (p AnyValue) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return p.Reconstruct(usage), nil
}

func (p StringAggregateProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return NewStringAggregateProvider(usage), nil
}

func (p *ArrayAggregateProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	argument := r.Type(p.ArgumentType())
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return NewArrayAggregateProvider(argument, usage, p.Arrays()), nil
}

func (p *HypotheticalSet) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return NewHypotheticalSet(usage, p.Operation()), nil
}

func (p *OrderedSet) Remap(r ProviderRemap) (AggregateProvider, error) {
	result, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	input, fraction := r.Type(p.InputType()), r.Type(p.FractionType())
	argument, err := r.Usage(r.Argument)
	if err != nil {
		return nil, err
	}
	comparison, err := sqltype.BindOrdering(argument)
	if err != nil {
		return nil, err
	}
	rebound, err := NewOrderedSet(result, input, fraction, p.Operation(), p.Array(), comparison)
	if err != nil {
		return nil, err
	}
	return rebound, nil
}

func (p IntegralBitwiseAggregateProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	bound, err := sqltype.BindBitwise(p.Operator(), usage.Type, usage.Type)
	if err != nil {
		return nil, err
	}
	return NewIntegralBitwiseAggregateProvider(usage, p.Operator(), bound), nil
}

func (p BitStringAggregateProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	return NewBitStringAggregateProvider(usage, p.Operator()), nil
}

func (p JSONArrayAggregateProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	input, err := r.Usage(p.InputUsage())
	if err != nil {
		return nil, err
	}
	result, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.BindJSONArrayAggregate(input, result, p.Absent())
	if err != nil {
		return nil, err
	}
	return NewJSONArrayAggregateProvider(input, result, p.Absent(), plan), nil
}

func (p JSONObjectAggregate) Remap(r ProviderRemap) (AggregateProvider, error) {
	key, err := r.Usage(p.KeyUsage())
	if err != nil {
		return nil, err
	}
	input, err := r.Usage(p.InputUsage())
	if err != nil {
		return nil, err
	}
	result, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	rebound, err := p.Reconstruct(key, input, result)
	if err != nil {
		return nil, err
	}
	return rebound, nil
}

func (p ExtremaProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	result, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	_, conflict := p.Comparison().InputCollation()
	comparison, err := sqltype.BindComparison(result.Type, result.Type, result.Collation, conflict)
	if err != nil {
		return nil, err
	}
	return NewExtremaProvider(result, comparison, p.Maximum()), nil
}

func (p RangeAggregateProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	input, err := r.Usage(p.InputUsage())
	if err != nil {
		return nil, err
	}
	result, err := r.Usage(p.ResultUsage())
	if err != nil {
		return nil, err
	}
	bound, err := sqltype.BindRangeAggregate(input.Type, p.Operation())
	if err != nil {
		return nil, err
	}
	if bound.ResultUsage() != result {
		return nil, fmt.Errorf("stuntgres: inconsistent remapped range aggregate result")
	}
	return NewRangeAggregateProvider(input, result, p.Operation(), bound), nil
}
