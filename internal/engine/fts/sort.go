package fts

import (
	"context"
	"slices"
)

// stableSort preserves input order for ties: the released position cutoff can
// retain the first weight at the 256th distinct position. Small sorts need no
// scratch allocation; larger sorts share one half-sized merge buffer. Only
// bounded runs use the non-cancellable library sort.
func stableSort[T any](ctx context.Context, values []T, compare func(T, T) int) error {
	if len(values) <= 128 {
		return stableMergeSort(ctx, values, nil, compare)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for i := 1; i < len(values); i++ {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if compare(values[i-1], values[i]) > 0 {
			return stableMergeSort(ctx, values, make([]T, len(values)/2), compare)
		}
	}
	return ctx.Err()
}

func stableMergeSort[T any](ctx context.Context, values, scratch []T, compare func(T, T) int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) <= 128 {
		slices.SortStableFunc(values, compare)
		return ctx.Err()
	}
	mid := len(values) / 2
	if err := stableMergeSort(ctx, values[:mid], scratch, compare); err != nil {
		return err
	}
	if err := stableMergeSort(ctx, values[mid:], scratch, compare); err != nil {
		return err
	}
	// Ordered halves need no merge, copying or additional allocation.
	if compare(values[mid-1], values[mid]) <= 0 {
		return ctx.Err()
	}
	for start := 0; start < mid; start += 256 {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+256, mid)
		copy(scratch[start:end], values[start:end])
	}
	left, right := 0, mid
	for out := 0; left < mid; out++ {
		if out&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if right < len(values) && compare(values[right], scratch[left]) < 0 {
			values[out] = values[right]
			right++
		} else {
			values[out] = scratch[left]
			left++
		}
	}
	return ctx.Err()
}
