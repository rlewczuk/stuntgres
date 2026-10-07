package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestUpdateCoreFloatIntegerAssignment(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, source := range []ID{Float4, Float8} {
		for _, target := range []ID{Int2, Int4, Int8} {
			plan, err := PlanCoercion(types[source].Usage(), types[target].Usage(), Assignment)
			if err != nil {
				t.Fatal(source, target, err)
			}
			if _, err := PlanCoercion(types[source].Usage(), types[target].Usage(), Implicit); err == nil {
				t.Fatal("implicit narrowing admitted")
			}
			for _, tc := range []struct {
				n    float64
				want int64
			}{{2.5, 2}, {3.5, 4}, {-2.5, -2}, {-3.5, -4}, {0, 0}} {
				value, err := Float(types[source], tc.n)
				if err != nil {
					t.Fatal(err)
				}
				got, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
				if err != nil {
					t.Fatal(err)
				}
				integer, err := got.Integer()
				if err != nil || integer != tc.want {
					t.Fatal(source, target, tc, integer, err)
				}
			}
			name := map[ID]string{Int2: "smallint", Int4: "integer", Int8: "bigint"}[target]
			for _, n := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
				v, _ := Float(types[source], n)
				_, err := plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
				numericProviderDiagnostic(t, err, sqlstate.Error{Code: "22003", Message: name + " out of range"})
			}
			null, err := plan.Apply(ctx, Null(types[source]), InputEnvironment{}, OutputEnvironment{})
			if err != nil || !null.IsNull() || null.Type() != types[target] {
				t.Fatal(null, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			value, _ := Float(types[source], 1)
			if _, err := plan.Apply(canceled, value, InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			foreign, _ := Float(testTypes()[source], 1)
			if _, err := plan.Apply(ctx, foreign, InputEnvironment{}, OutputEnvironment{}); err == nil {
				t.Fatal("foreign source descriptor accepted")
			}
		}
	}
	for _, tc := range []struct {
		source, target ID
		n              float64
		want           int64
		fail           bool
	}{
		{Float8, Int2, 32767.49, 32767, false}, {Float8, Int2, 32767.5, 0, true}, {Float8, Int2, -32768.5, -32768, false}, {Float8, Int2, -32768.51, 0, true},
		{Float4, Int4, float64(math.Float32frombits(0x4effffff)), 2147483520, false}, {Float4, Int4, 2147483647, 0, true},
		{Float8, Int4, 2147483647.49, 2147483647, false}, {Float8, Int4, 2147483647.5, 0, true}, {Float8, Int4, -2147483648.5, -2147483648, false},
		{Float8, Int8, math.Nextafter(9223372036854775808, 0), 9223372036854774784, false}, {Float8, Int8, 9223372036854775808, 0, true}, {Float8, Int8, -9223372036854775808, -9223372036854775808, false}, {Float8, Int8, math.Nextafter(-9223372036854775808, math.Inf(-1)), 0, true},
		{Float4, Int8, float64(math.Float32frombits(0x5effffff)), 9223371487098961920, false}, {Float4, Int8, 9223372036854775808, 0, true},
	} {
		plan, err := PlanCoercion(types[tc.source].Usage(), types[tc.target].Usage(), Assignment)
		if err != nil {
			t.Fatal(err)
		}
		value, err := Float(types[tc.source], tc.n)
		if err != nil {
			t.Fatal(err)
		}
		got, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		if tc.fail {
			name := map[ID]string{Int2: "smallint", Int4: "integer", Int8: "bigint"}[tc.target]
			numericProviderDiagnostic(t, err, sqlstate.Error{Code: "22003", Message: name + " out of range"})
			continue
		}
		integer, e := got.Integer()
		if err != nil || e != nil || integer != tc.want {
			t.Fatal(tc, integer, err, e)
		}
	}
}
