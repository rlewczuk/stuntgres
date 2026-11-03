package exec

import (
	"context"
	"math/rand/v2"
	"sort"

	"stuntgres/internal/engine/plan"
)

func sampledSource(node plan.Node) bool {
	source, ok := node.(*plan.SourceNode)
	return ok && source.Sample != nil
}

func openSampleStage(ctx context.Context, state *State, n *plan.SourceNode) (Stage, error) {
	evaluation := state.Evaluation(ctx)
	value, err := n.Sample.Count.Eval(&evaluation)
	evaluation.Context = nil
	if err != nil {
		return Stage{}, err
	}
	if value.IsNull() {
		return Stage{}, sqlError("2202H", "TABLESAMPLE parameter cannot be null")
	}
	count, err := value.Integer()
	if err != nil {
		return Stage{}, err
	}
	if count < 0 {
		return Stage{}, sqlError("2202H", "sample size must not be negative")
	}
	child, err := initializeRelationSource(ctx, state, n)
	if err != nil {
		return Stage{}, err
	}
	out := Stage{Source: child.Source, Columns: n.Schema(), Finish: child.Close, Pending: child.Pending}
	if err := selectSampleRows(ctx, &out, &child, count); err != nil {
		child.Close()
		return Stage{}, err
	}
	out.Reset = func(ctx context.Context, out *Stage) (bool, error) {
		changed, err := child.Rewind(ctx)
		if err != nil || !changed {
			return changed, err
		}
		// A changed child is a new invocation. The accepted expression and
		// source stay immutable; only this stage's visible identities change.
		evaluation := state.Evaluation(ctx)
		value, err := n.Sample.Count.Eval(&evaluation)
		evaluation.Context = nil
		if err != nil {
			return false, err
		}
		if value.IsNull() {
			return false, sqlError("2202H", "TABLESAMPLE parameter cannot be null")
		}
		count, err := value.Integer()
		if err != nil {
			return false, err
		}
		if count < 0 {
			return false, sqlError("2202H", "sample size must not be negative")
		}
		return true, selectSampleRows(ctx, out, &child, count)
	}
	return out, nil
}

// Reservoir entries are visible Stage occurrences, never raw version offsets.
// The finite visible population bounds every conversion and allocation. No
// physical heap-block distribution or cross-execution seed is promised here.
func selectSampleRows(ctx context.Context, out, child *Stage, count int64) error {
	out.Selection, out.CacheOccurrences, out.OrderValues = nil, nil, nil
	out.Permutation = nil
	out.scanRange, out.scanStart, out.scanEnd = false, 0, 0
	if count == 0 {
		out.Selection = []int{}
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Positive sampling consumes the child; initialization and zero sampling
	// do not. Empty sources remain ineligible, and hierarchy Drain reaches
	// each physical segment through its existing stage-local frontier.
	child.beginRead()
	if err := child.Drain(ctx); err != nil {
		return err
	}
	out.Source, out.CacheWidth, out.OrderValues = child.Source, child.CacheWidth, child.OrderValues
	population := child.Count()
	if count >= int64(population) {
		out.Selection, out.Permutation = child.Selection, child.Permutation
		out.CacheOccurrences = child.CacheOccurrences
		out.scanRange, out.scanStart, out.scanEnd = child.scanRange, child.scanStart, child.scanEnd
		return ctx.Err()
	}
	// A hierarchy represents separately sampled physical RTEs, not one
	// reservoir over their concatenation. Security-filtered occurrences still
	// retain their original physical references and contiguous source groups.
	size := 0
	for start := 0; start < population; {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := sampleGroupEnd(child, start)
		size += int(min(count, int64(end-start)))
		start = end
	}
	if size == population {
		out.Selection, out.Permutation = child.Selection, child.Permutation
		out.CacheOccurrences = child.CacheOccurrences
		out.scanRange, out.scanStart, out.scanEnd = child.scanRange, child.scanStart, child.scanEnd
		return ctx.Err()
	}
	out.Selection = make([]int, size)
	if child.CacheWidth != 0 {
		out.CacheOccurrences = make([]int, size)
	}
	base := 0
	for start := 0; start < population; {
		end := sampleGroupEnd(child, start)
		kept := int(min(count, int64(end-start)))
		for i := start; i < end; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			slot := i - start
			if slot >= kept {
				slot = rand.IntN(slot + 1)
			}
			if slot < kept {
				out.Selection[base+slot] = child.Index(i)
				if child.CacheWidth != 0 {
					out.CacheOccurrences[base+slot] = child.orderOccurrence(i)
				}
			}
		}
		base += kept
		start = end
	}
	return ctx.Err()
}

// Source opening and row security preserve physical-source order. Find group
// boundaries over visible Stage ordinals without allocating a parallel map or
// changing shared visibility/layout vectors.
func sampleGroupEnd(child *Stage, start int) int {
	if child.Source.Inheritance == nil {
		return child.Count()
	}
	ref := child.Source.PhysicalRef(child.Index(start))
	return start + sort.Search(child.Count()-start, func(offset int) bool {
		return child.Source.PhysicalRef(child.Index(start+offset)) != ref
	})
}
