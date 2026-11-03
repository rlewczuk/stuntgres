package fts

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestFTSRankOperandSorting(t *testing.T) {
	for _, n := range []int{6, 7, 8, 40, 41} {
		// Released right-first input: a leading z forces the nonsorted path;
		// equal cat operands carry interleaved observable prefix identities.
		nodes := make([]queryNode, n)
		input := make([]int, n)
		for i := range nodes {
			nodes[i] = queryNode{op: 'v', word: "cat", prefix: i >= 3 && i%2 == 1}
			input[i] = i
		}
		nodes[0].word = "z"
		before := slices.Clone(nodes)
		want := make([]int, 0, n)
		switch n {
		case 6:
			want = []int{1, 2, 3, 4, 5, 0} // stable insertion, no equal swaps
		case 7:
			want = []int{3, 1, 2, 6, 4, 5, 0} // exact middle pivot, equal partition swaps
		default:
			// Median3 (8/40) and ninther (41) select the final cat here.
			want = append(want, n-1)
			for i := 1; i < n-1; i++ {
				want = append(want, i)
			}
			want = append(want, 0)
		}
		items := slices.Clone(input)
		if err := rankSortOperands(t.Context(), items, nodes); err != nil || !slices.Equal(items, want) {
			t.Fatal("released qsort_arg permutation", n, items, want, err)
		}
		if !slices.Equal(nodes, before) {
			t.Fatal("sort mutated query nodes")
		}
		baseline := &countingContext{Context: t.Context(), after: int(^uint(0) >> 1)}
		if err := rankSortOperands(baseline, slices.Clone(input), nodes); err != nil {
			t.Fatal(err)
		}
		for after := 1; after <= baseline.calls; after++ {
			ctx := &countingContext{Context: t.Context(), after: after}
			if err := rankSortOperands(ctx, slices.Clone(input), nodes); !errors.Is(err, context.Canceled) {
				t.Fatal("sort cancellation", n, after, err)
			}
			if !slices.Equal(nodes, before) {
				t.Fatal("canceled sort mutated source arena")
			}
		}
	}
}
