package enginetests

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPgFreespaceLayout(t *testing.T) {
	for _, tc := range []struct{ payload, blocks, last int64 }{
		{0, 0, 0}, {1, 1, 8160}, {8, 1, 8160}, {9, 1, 8128}, {40, 1, 8128}, {41, 1, 8096},
		{44, 1, 8096}, {88, 1, 8064}, {8096, 1, 64}, {8140, 1, 0}, {8168, 1, 0}, {8169, 2, 8160}, {8184, 2, 8128}, {8228, 2, 8096},
		{35081292865560, 4294967295, 0},
	} {
		layout, err := storage.FreespacePayload(t.Context(), tc.payload)
		if err != nil || layout.Blocks != tc.blocks || layout.LastAvail != tc.last {
			t.Fatal(tc, layout, err)
		}
		if layout.Probe(-1) != 0 || layout.Probe(tc.blocks) != 0 || layout.Probe(math.MaxInt64) != 0 {
			t.Fatal("absent probe grew layout", layout)
		}
		if tc.blocks > 0 && layout.Probe(tc.blocks-1) != tc.last {
			t.Fatal("last probe", tc, layout)
		}
		if tc.blocks > 1 && layout.Probe(0) != 0 {
			t.Fatal("full page exposed within-page free space")
		}
	}
	if _, err := storage.FreespacePayload(t.Context(), -1); err == nil {
		t.Fatal("negative payload accepted")
	}
	var diagnostic *sqlstate.Error
	if _, err := storage.AddSize(math.MaxInt64, 1); !errors.As(err, &diagnostic) || diagnostic.Code != "22003" {
		t.Fatal("unchecked row accumulation", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := storage.FreespacePayload(ctx, 44)
	if !errors.Is(err, context.Canceled) || result != (storage.FreespaceLayout{}) {
		t.Fatal("canceled layout published prefix", result, err)
	}
}
