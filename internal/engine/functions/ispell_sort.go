package functions

import (
	"context"
	"slices"
	"strings"
)

// Like the owning FTS codec's private stable sort, bound library-sort runs and
// poll while merging. Compilation must not make cancellation wait for an
// unbounded library sort. Scratch is private to this one initialization.
func ispellSort[T any](ctx context.Context, values []T, compare func(T, T) int) error {
	var scratch []T
	if len(values) > 128 {
		scratch = make([]T, len(values)/2)
	}
	return ispellMergeSort(ctx, values, scratch, compare)
}
func ispellMergeSort[T any](ctx context.Context, values, scratch []T, compare func(T, T) int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) <= 128 {
		slices.SortStableFunc(values, compare)
		return ctx.Err()
	}
	mid := len(values) / 2
	if err := ispellMergeSort(ctx, values[:mid], scratch, compare); err != nil {
		return err
	}
	if err := ispellMergeSort(ctx, values[mid:], scratch, compare); err != nil {
		return err
	}
	if compare(values[mid-1], values[mid]) <= 0 {
		return ctx.Err()
	}
	for start := 0; start < mid; start += 256 {
		if err := ctx.Err(); err != nil {
			return err
		}
		copy(scratch[start:min(start+256, mid)], values[start:min(start+256, mid)])
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

// Stop words can have arbitrarily long common byte prefixes. Compare bounded
// spans so even a bounded library-sort run cannot hide cancellation. Returning
// equality after cancellation is private: the enclosing sort rejects the result.
func ispellCompare(ctx context.Context, a, b string) int {
	for len(a) > 4096 && len(b) > 4096 {
		if err := ctx.Err(); err != nil {
			return 0
		}
		if c := strings.Compare(a[:4096], b[:4096]); c != 0 {
			return c
		}
		a, b = a[4096:], b[4096:]
	}
	if err := ctx.Err(); err != nil {
		return 0
	}
	return strings.Compare(a, b)
}
