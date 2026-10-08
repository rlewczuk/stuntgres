package sqltype

import "context"

// binarySearchWork bounds cancellation latency throughout factorization and
// matching, not merely between candidate positions. The counter is stack-owned.
type binarySearchWork struct {
	ctx       context.Context
	remaining int
}

func (w *binarySearchWork) step() error {
	w.remaining--
	if w.remaining == 0 {
		w.remaining = 4096
		return w.ctx.Err()
	}
	return nil
}

// binaryMaximalSuffix finds a critical-factorization candidate in one byte
// ordering. Reversing the ordering and taking the larger suffix gives the cut.
func binaryMaximalSuffix(needle string, reverse bool, work *binarySearchWork) (int, int, error) {
	suffix, candidate, offset, period := -1, 0, 1, 1
	for candidate+offset < len(needle) {
		if err := work.step(); err != nil {
			return 0, 0, err
		}
		a, b := needle[candidate+offset], needle[suffix+offset]
		before := a < b
		if reverse {
			before = a > b
		}
		switch {
		case before:
			candidate += offset
			offset = 1
			period = candidate - suffix
		case a == b:
			if offset == period {
				candidate += period
				offset = 1
			} else {
				offset++
			}
		default:
			suffix = candidate
			candidate = suffix + 1
			offset, period = 1, 1
		}
	}
	return suffix, period, nil
}

// binaryPosition is the constant-space Two-Way string matcher. Critical
// factorization and remembered periodic prefixes give O(haystack+needle) work,
// including repetitive absent patterns, without a needle-sized scratch table.
// It returns a zero-based index, or -1; SQL applies its one-based convention.
func binaryPosition(ctx context.Context, haystack, needle string) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	if len(needle) == 0 {
		return 0, nil
	}
	if len(needle) > len(haystack) {
		return -1, nil
	}
	work := binarySearchWork{ctx: ctx, remaining: 4096}
	left, period, err := binaryMaximalSuffix(needle, false, &work)
	if err != nil {
		return -1, err
	}
	right, otherPeriod, err := binaryMaximalSuffix(needle, true, &work)
	if err != nil {
		return -1, err
	}
	if right > left {
		left, period = right, otherPeriod
	}
	cut := left + 1
	periodic := period+cut <= len(needle)
	if periodic {
		for i := range cut {
			if err := work.step(); err != nil {
				return -1, err
			}
			if needle[i] != needle[period+i] {
				periodic = false
				break
			}
		}
	}
	rememberedAfterShift := 0
	if periodic {
		rememberedAfterShift = len(needle) - period
	} else {
		period = max(left, len(needle)-left-1) + 1
	}
	memory := 0
	for start := 0; start <= len(haystack)-len(needle); {
		if err := work.step(); err != nil {
			return -1, err
		}
		i := max(cut, memory)
		for i < len(needle) {
			if err := work.step(); err != nil {
				return -1, err
			}
			if needle[i] != haystack[start+i] {
				break
			}
			i++
		}
		if i < len(needle) {
			start += i - cut + 1
			memory = 0
			continue
		}
		i = cut
		for i > memory {
			if err := work.step(); err != nil {
				return -1, err
			}
			if needle[i-1] != haystack[start+i-1] {
				break
			}
			i--
		}
		if i <= memory {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
			return start, nil
		}
		start += period
		memory = rememberedAfterShift
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	return -1, nil
}
