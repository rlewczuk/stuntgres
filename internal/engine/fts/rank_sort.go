package fts

import (
	"context"
	"strings"
)

// rankSortOperands reproduces REL_18_6 qsort_arg/sort_template.h, including
// equality swaps: dedup retains the first equal query operand, whose prefix is
// observable. Only invocation-owned indices move, never immutable query nodes.
func rankSortOperands(ctx context.Context, items []int, nodes []queryNode) error {
	compare := func(a, b int) int { return strings.Compare(nodes[items[a]].word, nodes[items[b]].word) }
	median := func(a, b, c int) int {
		if compare(a, b) < 0 {
			if compare(b, c) < 0 {
				return b
			}
			if compare(a, c) < 0 {
				return c
			}
			return a
		}
		if compare(b, c) > 0 {
			return b
		}
		if compare(a, c) < 0 {
			return a
		}
		return c
	}
	swapRange := func(a, b, n int) error {
		for i := 0; i < n; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			items[a+i], items[b+i] = items[b+i], items[a+i]
		}
		return nil
	}
	a, n := 0, len(items)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n < 7 {
			for pm := a + 1; pm < a+n; pm++ {
				for pl := pm; pl > a && compare(pl-1, pl) > 0; pl-- {
					if err := ctx.Err(); err != nil {
						return err
					}
					items[pl], items[pl-1] = items[pl-1], items[pl]
				}
			}
			return ctx.Err()
		}
		presorted := true
		for pm := a + 1; pm < a+n; pm++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if compare(pm-1, pm) > 0 {
				presorted = false
				break
			}
		}
		if presorted {
			return ctx.Err()
		}
		pm := a + n/2
		if n > 7 {
			pl, pn := a, a+n-1
			if n > 40 {
				d := n / 8
				pl = median(pl, pl+d, pl+2*d)
				pm = median(pm-d, pm, pm+d)
				pn = median(pn-2*d, pn-d, pn)
			}
			pm = median(pl, pm, pn)
		}
		items[a], items[pm] = items[pm], items[a]
		pa, pb, pc, pd := a+1, a+1, a+n-1, a+n-1
		for {
			for pb <= pc {
				r := compare(pb, a)
				if r > 0 {
					break
				}
				if r == 0 {
					items[pa], items[pb] = items[pb], items[pa]
					pa++
				}
				pb++
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			for pb <= pc {
				r := compare(pc, a)
				if r < 0 {
					break
				}
				if r == 0 {
					items[pc], items[pd] = items[pd], items[pc]
					pd--
				}
				pc--
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if pb > pc {
				break
			}
			items[pb], items[pc] = items[pc], items[pb]
			pb++
			pc--
		}
		pn := a + n
		d1 := min(pa-a, pb-pa)
		if err := swapRange(a, pb-d1, d1); err != nil {
			return err
		}
		d1 = min(pd-pc, pn-pd-1)
		if err := swapRange(pb, pn-d1, d1); err != nil {
			return err
		}
		d1, d2 := pb-pa, pd-pc
		if d1 <= d2 {
			if d1 > 1 {
				if err := rankSortOperands(ctx, items[a:a+d1], nodes); err != nil {
					return err
				}
			}
			if d2 > 1 {
				a, n = pn-d2, d2
				continue
			}
		} else {
			if d2 > 1 {
				if err := rankSortOperands(ctx, items[pn-d2:pn], nodes); err != nil {
					return err
				}
			}
			if d1 > 1 {
				n = d1
				continue
			}
		}
		return ctx.Err()
	}
}
