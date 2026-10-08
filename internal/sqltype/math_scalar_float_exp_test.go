package sqltype

import (
	"context"
	"math"
	"testing"
)

func TestMathScalarExpSubnormals(t *testing.T) {
	// Independently rounded 180-digit decimal exponentials of the exact
	// represented FLOAT8 inputs. Root observed nonzero-subnormal success on
	// pinned PostgreSQL; these constants are not produced with math.Exp.
	for _, tc := range []struct {
		input string
		bits  uint64
	}{
		{"-708.3964185322642", 0x000ffffffffffe7c},
		{"-709", 0x0008bfe55de02338},
		{"-744", 0x0000000000000002},
		{"-745", 0x0000000000000001},
		{"-745.1332191019411", 0x0000000000000001},
	} {
		t.Run(tc.input, func(t *testing.T) {
			p, v := scalarProvider(t, "exp", Float8, tc.input)
			got, err := p.Evaluate(context.Background(), v[0], v[1])
			if err != nil || math.Float64bits(got.floating) != tc.bits {
				t.Fatalf("exp(%s) actual=%g bits=%016x want=%016x error=%v", tc.input, got.floating, math.Float64bits(got.floating), tc.bits, err)
			}
		})
	}
	p, v := scalarProvider(t, "exp", Float8, "-745.1332191019412")
	_, err := p.Evaluate(context.Background(), v[0], v[1])
	scalarError(t, err, "22003", "value out of range: underflow")
}
