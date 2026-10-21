package functions

import (
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

// remapGenerations maps every builtin descriptor of one generation to the
// descriptor with the same ID in a fresh generation.
type remapGenerations struct {
	source, target map[sqltype.ID]*sqltype.Descriptor
}

const remapInt4Array sqltype.ID = 1007

func newRemapGenerations(t *testing.T) remapGenerations {
	t.Helper()
	g := remapGenerations{source: remapGeneration(t), target: remapGeneration(t)}
	return g
}

func remapGeneration(t *testing.T) map[sqltype.ID]*sqltype.Descriptor {
	t.Helper()
	generation := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		generation[d.ID()] = d
	}
	array, err := sqltype.Array(remapInt4Array, "_int4", generation[sqltype.Int4])
	if err != nil {
		t.Fatal(err)
	}
	generation[remapInt4Array] = array
	return generation
}

func (g remapGenerations) usage(id sqltype.ID) sqltype.Usage { return g.source[id].Usage() }

func (g remapGenerations) remap(fail func(sqltype.Usage) error) ProviderRemap {
	typ := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d != nil && g.source[d.ID()] == d {
			return g.target[d.ID()]
		}
		return d
	}
	return ProviderRemap{
		Type: typ,
		Usage: func(u sqltype.Usage) (sqltype.Usage, error) {
			u.Type = typ(u.Type)
			if fail != nil {
				if err := fail(u); err != nil {
					return sqltype.Usage{}, err
				}
			}
			return u, nil
		},
	}
}

func (g remapGenerations) providers(t *testing.T) map[string]RemappableProvider {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	int4, int8, float8, numeric := g.source[sqltype.Int4], g.source[sqltype.Int8], g.source[sqltype.Float8], g.source[sqltype.Numeric]
	money, interval := g.source[sqltype.Money], g.source[sqltype.Interval]
	moneyAdd, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, money, money, money)
	must(err)
	bitwise, err := sqltype.BindBitwise(sqltype.BitwiseOr, int4, int4)
	must(err)
	jsonArray, err := sqltype.BindJSONArrayAggregate(g.usage(sqltype.Int4), g.usage(sqltype.JSON), false)
	must(err)
	jsonObject, err := NewJSONObjectAggregate(g.usage(sqltype.JSON), false, false).Reconstruct(g.usage(sqltype.Text), g.usage(sqltype.Int4), g.usage(sqltype.JSON))
	must(err)
	extremaComparison, err := sqltype.BindComparison(int4, int4, sqltype.CollationRef{}, false)
	must(err)
	ordering, err := sqltype.BindOrdering(g.usage(sqltype.Float8))
	must(err)
	ordered, err := NewOrderedSet(g.usage(sqltype.Float8), float8, float8, AggregatePercentileCont, false, ordering)
	must(err)
	rangeBound, err := sqltype.BindRangeAggregate(g.source[sqltype.Int4Range], sqltype.RangeAggregateIntersection)
	must(err)
	return map[string]RemappableProvider{
		"IntegralSum":          NewIntegralSum(int4, g.usage(sqltype.Int8)),
		"NumericSum":           NewNumericSum(int8, g.usage(sqltype.Numeric)),
		"FloatSum":             NewFloatSum(float8, g.usage(sqltype.Float8)),
		"MoneySum":             NewMoneySum(g.usage(sqltype.Money), moneyAdd),
		"IntervalSum":          NewIntervalSum(interval.Usage()),
		"IntervalAvg":          NewIntervalAvg(interval.Usage()),
		"NumericAvg":           NewNumericAvg(numeric, g.usage(sqltype.Numeric)),
		"FloatAvg":             NewFloatAvg(float8, g.usage(sqltype.Float8)),
		"StatisticalAggregate": NewStatisticalAggregate(float8, g.usage(sqltype.Float8), true, false),
		"RegressionAggregate":  NewRegressionAggregate(float8, g.usage(sqltype.Float8), sqltype.RegressionSlope),
		"RegressionCount":      NewRegressionCountAggregate(float8, g.usage(sqltype.Int8)),
		"Count":                NewCountProvider(g.usage(sqltype.Int8)),
		"Boolean":              NewBooleanAggregateProvider(g.usage(sqltype.Bool), true),
		"AnyValue":             NewAnyValue(g.usage(sqltype.Int4)),
		"StringAggregate":      NewStringAggregateProvider(g.usage(sqltype.Text)),
		"ArrayAggregate":       NewArrayAggregateProvider(int4, g.usage(remapInt4Array), false),
		"HypotheticalSet":      NewHypotheticalSet(g.usage(sqltype.Int8), AggregateHypotheticalRank),
		"OrderedSet":           ordered,
		"IntegralBitwise":      NewIntegralBitwiseAggregateProvider(g.usage(sqltype.Int4), sqltype.BitwiseOr, bitwise),
		"BitString":            NewBitStringAggregateProvider(g.usage(sqltype.VarBit), sqltype.BitwiseAnd),
		"JSONArray":            NewJSONArrayAggregateProvider(g.usage(sqltype.Int4), g.usage(sqltype.JSON), false, jsonArray),
		"JSONObject":           jsonObject,
		"Extrema":              NewExtremaProvider(g.usage(sqltype.Int4), extremaComparison, true),
		"Range":                NewRangeAggregateProvider(g.usage(sqltype.Int4Range), g.usage(sqltype.Int4Range), sqltype.RangeAggregateIntersection, rangeBound),
	}
}

// remappedDescriptors lists every descriptor a provider exposes.
func remappedDescriptors(p AggregateProvider) []*sqltype.Descriptor {
	out := []*sqltype.Descriptor{p.ResultUsage().Type}
	if p, ok := p.(interface{ InputType() *sqltype.Descriptor }); ok {
		out = append(out, p.InputType())
	}
	if p, ok := p.(interface{ FractionType() *sqltype.Descriptor }); ok {
		out = append(out, p.FractionType())
	}
	if p, ok := p.(interface{ ArgumentType() *sqltype.Descriptor }); ok {
		out = append(out, p.ArgumentType())
	}
	if p, ok := p.(interface{ InputUsage() sqltype.Usage }); ok {
		out = append(out, p.InputUsage().Type)
	}
	if p, ok := p.(interface{ KeyUsage() sqltype.Usage }); ok {
		out = append(out, p.KeyUsage().Type)
	}
	return out
}

func TestAggregateProviderRemapMapsEveryDescriptor(t *testing.T) {
	g := newRemapGenerations(t)
	for name, p := range g.providers(t) {
		r := g.remap(nil)
		r.Argument = g.usage(sqltype.Float8)
		next, err := p.Remap(r)
		if err != nil {
			t.Fatal(name, err)
		}
		for _, d := range remappedDescriptors(next) {
			if d == nil || g.target[d.ID()] != d {
				t.Fatalf("%s kept an unmapped descriptor %v", name, d)
			}
		}
		for _, d := range remappedDescriptors(p) {
			if d == nil || g.source[d.ID()] != d {
				t.Fatalf("%s remap changed the source provider: %v", name, d)
			}
		}
	}
}

func TestAggregateProviderRemapReturnsUsageError(t *testing.T) {
	g := newRemapGenerations(t)
	failure := errors.New("collation remap failed")
	for name, p := range g.providers(t) {
		r := g.remap(func(sqltype.Usage) error { return failure })
		r.Argument = g.usage(sqltype.Float8)
		next, err := p.Remap(r)
		if !errors.Is(err, failure) || next != nil {
			t.Fatalf("%s: got %v, %v", name, next, err)
		}
	}
}

func TestAggregateProviderRemapOrderedSetArgument(t *testing.T) {
	g := newRemapGenerations(t)
	ordered := g.providers(t)["OrderedSet"]
	failure := errors.New("argument collation remap failed")
	r := g.remap(func(u sqltype.Usage) error {
		if u.Type.ID() == sqltype.Text {
			return failure
		}
		return nil
	})
	r.Argument = g.usage(sqltype.Text)
	if _, err := ordered.Remap(r); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestAggregateProviderRemapRangeInconsistentResult(t *testing.T) {
	g := newRemapGenerations(t)
	bound, err := sqltype.BindRangeAggregate(g.source[sqltype.Int4Range], sqltype.RangeAggregateUnion)
	if err != nil {
		t.Fatal(err)
	}
	p := NewRangeAggregateProvider(g.usage(sqltype.Int4Range), g.usage(sqltype.Int8), sqltype.RangeAggregateUnion, bound)
	next, err := p.Remap(g.remap(nil))
	if err == nil || err.Error() != "stuntgres: inconsistent remapped range aggregate result" || next != nil {
		t.Fatal(next, err)
	}
}
