package enginetests

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPgstatindexApproximation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		weight, leaves, internal, level, root, size int64
		density                                     float64
	}{
		{0, 0, 0, 0, 0, 8192, math.NaN()},
		{20, 1, 0, 0, 1, 16384, 0.25},
		{28, 1, 0, 0, 1, 16384, 0.34},
		{36, 1, 0, 0, 1, 16384, 0.44},
		{8148, 1, 0, 0, 1, 16384, 99.95},
		{8176, 2, 1, 1, 3, 32768, 50.15},
		{8152 * 256, 256, 1, 1, 257, 258 * 8192, 100},
		{8152 * 257, 257, 3, 2, 260, 261 * 8192, 100},
	} {
		s, err := index.CompactBtreeLayout(ctx, tc.weight)
		if err != nil || s.Version != 4 || s.Leaves != tc.leaves || s.Internal != tc.internal || s.Level != tc.level || s.Root != tc.root || s.Size != tc.size || s.Empty != 0 || s.Deleted != 0 || s.Weight != tc.weight {
			t.Fatalf("weight %d: %+v %v", tc.weight, s, err)
		}
		if math.IsNaN(tc.density) {
			if !math.IsNaN(s.Density) || !math.IsNaN(s.Fragmentation) {
				t.Fatal(s)
			}
		} else if s.Density != tc.density || s.Fragmentation != 0 || math.Signbit(s.Fragmentation) {
			t.Fatal(s)
		}
	}
	for _, weight := range []int64{-1, math.MaxInt64} {
		_, err := index.CompactBtreeLayout(ctx, weight)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22003" || d.Message != "bigint out of range" {
			t.Fatal(weight, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := index.CompactBtreeLayout(canceled, 28); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := index.CapturePgstattupleStats(canceled, index.PgstattupleStatsCapture{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
