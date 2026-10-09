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

// binaryMatcher prepares one constant-space Two-Way factorization. Replacement
// reuses it across every nonoverlapping match and both output passes.
type binaryMatcher struct {
	needle                            string
	cut, period, rememberedAfterShift int
}

func prepareBinaryMatcher(needle string, work *binarySearchWork) (binaryMatcher, error) {
	if err := work.ctx.Err(); err != nil {
		return binaryMatcher{}, err
	}
	if len(needle) == 0 {
		return binaryMatcher{needle: needle}, nil
	}
	left, period, err := binaryMaximalSuffix(needle, false, work)
	if err != nil {
		return binaryMatcher{}, err
	}
	right, otherPeriod, err := binaryMaximalSuffix(needle, true, work)
	if err != nil {
		return binaryMatcher{}, err
	}
	if right > left {
		left, period = right, otherPeriod
	}
	cut := left + 1
	periodic := period+cut <= len(needle)
	if periodic {
		for i := range cut {
			if err := work.step(); err != nil {
				return binaryMatcher{}, err
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
	return binaryMatcher{needle, cut, period, rememberedAfterShift}, work.ctx.Err()
}

// Position returns a zero-based byte index, or -1. Its execution work counter
// includes comparisons across successive searches; no scratch survives a call.
func (m binaryMatcher) position(haystack string, work *binarySearchWork) (int, error) {
	if err := work.ctx.Err(); err != nil {
		return -1, err
	}
	needle, cut, period := m.needle, m.cut, m.period
	if len(needle) == 0 {
		return 0, nil
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
			if err := work.ctx.Err(); err != nil {
				return -1, err
			}
			return start, nil
		}
		start += period
		memory = m.rememberedAfterShift
	}
	if err := work.ctx.Err(); err != nil {
		return -1, err
	}
	return -1, nil
}

// binaryPosition retains the raw provider's empty/long-needle shortcuts.
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
	matcher, err := prepareBinaryMatcher(needle, &work)
	if err != nil {
		return -1, err
	}
	return matcher.position(haystack, &work)
}
