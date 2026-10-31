package enginetests

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPgstathashindexApproximation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct { n, overflow, freeBytes, capacity int64 }{
		{0, 0, 32608, 32608}, {1, 0, 32588, 32608},
		{4, 0, 32528, 32608}, {5, 0, 32508, 32608},
		{407, 0, 24468, 32608}, {408, 0, 24448, 32608},
		{1628, 0, 48, 32608}, {1629, 1, 8180, 40760},
		{2035, 1, 60, 40760}, {2036, 2, 8192, 48912},
	} {
		s, err := index.CompactHashLayout(ctx, tc.n, 0)
		if err != nil || s.Version != 4 || s.Buckets != 4 || s.Overflow != tc.overflow || s.Bitmap != 1 || s.Unused != 0 || s.LiveItems != tc.n || s.DeadItems != 0 || s.Weight != 20*tc.n || s.FreePercent != 100*float64(tc.freeBytes)/float64(tc.capacity) {
			t.Fatalf("N=%d: %+v %v", tc.n, s, err)
		}
	}
	for _, tc := range [][2]int64{{-1, 0}, {0, -1}, {math.MaxInt64, 1}, {math.MaxInt64/20+1, 0}} {
		_, err := index.CompactHashLayout(ctx, tc[0], tc[1])
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22003" || d.Message != "bigint out of range" { t.Fatal(tc, err) }
	}
	if _, err := index.CompactHashLayout(ctx, math.MaxInt64/40, 0); err != nil { t.Fatal("large constant-space model", err) }
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := index.CompactHashLayout(canceled, 1, 0); !errors.Is(err, context.Canceled) { t.Fatal(err) }
}
