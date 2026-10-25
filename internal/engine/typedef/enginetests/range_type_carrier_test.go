package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

// This inventory lives above routine/functions layering so its custom descriptors
// come from genuine catalog support, rather than fabricated lower-layer captures.
func rangeTypeUnitCarriers(t *testing.T, r, m *sqltype.Descriptor) {
	t.Helper()
	ctx := context.Background()
	rangeValue := rangeTypeUnitInput(t, r, "[1,3]")
	multiValue := rangeTypeUnitInput(t, m, "{[1,3],[8,9]}")
	domain, err := sqltype.Domain(920001, "carrier_span", "public.carrier_span", r.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(rangeValue)
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(920002, "_carrier_spans", r)
	if err != nil {
		t.Fatal(err)
	}
	elements, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, []sqltype.Value{rangeValue, sqltype.Null(r)})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []sqltype.Value{rangeValue, multiValue, wrapped, elements} {
		provider := functions.NewAnyValue(value.Type().Usage())
		state, err := provider.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		isolated, err := provider.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, next := range []sqltype.Value{sqltype.Null(value.Type()), value, sqltype.Null(value.Type())} {
			if err := provider.Advance(ctx, state, []sqltype.Value{next, {}}, nil); err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			got, err := provider.Finalize(ctx, state)
			if err != nil || !sqltype.SameDatum(got, value) {
				t.Fatal("custom carrier reconstructed value", got, err)
			}
			got, err = provider.Finalize(ctx, isolated)
			if err != nil || !got.IsNull() || got.Type() != value.Type() {
				t.Fatal("custom carrier group leaked", got, err)
			}
		}
	}
	for _, typ := range []*sqltype.Descriptor{r, m, domain, array} {
		if _, err := sqltype.BindOrdering(typ.Usage()); err == nil {
			t.Fatal("carrier fixture granted unsupported SQL ordering", typ.Name())
		}
	}
}
