package exec

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// QueryBranchInterval addresses captured visible source ordinals, not copied
// rows. Admission, locks and source lifetime remain owned by the full source.
type QueryBranchInterval struct{ Start, End int }

type QueryBranchSelection struct {
	Applied   bool
	Intervals []QueryBranchInterval
	EmptySegments []int // Selected empty physical leaves, lost by ordinal intervals.
}

// QueryBranchPruner is optional captured metadata authority. Selection is made
// unlocked, once per execution/filter opening, and never belongs to a held plan.
type QueryBranchPruner interface {
	Select(context.Context, *Context, expr.Expr, bool) (QueryBranchSelection, error)
}

type queryBranchCursor struct {
	selection QueryBranchSelection
	position  int
	emptyPosition int
}

func selectQueryBranches(ctx context.Context, state *State, filter *plan.FilterNode, child *Stage) (queryBranchCursor, error) {
	if sampledSource(filter.Child) {
		return queryBranchCursor{}, nil
	}
	if _, ok := filter.Child.(*plan.SourceNode); !ok || child.Produce != nil || child.Source.BranchPruner == nil || child.Source.RlsDenied || child.Source.RLS != nil || QueryHasMandatoryPhysicalFrontier(filter.Child, state.Source) {
		return queryBranchCursor{}, nil
	}
	evaluation := state.Evaluation(ctx)
	selection, err := child.Source.BranchPruner.Select(ctx, &evaluation, filter.Predicate, !state.Methods.NoPartitionPruning)
	if selection.Applied && child.scan != nil {
		child.scan.pruned = true
	}
	return queryBranchCursor{selection: selection}, err
}

func (c *queryBranchCursor) next(ctx context.Context, child *Stage) (int, bool, error) {
	if !c.selection.Applied {
		return child.NextOccurrence(ctx)
	}
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	start, end := 0, child.Source.Count()
	if child.scanRange {
		start, end = child.scanStart, child.scanEnd
	}
	for c.position < len(c.selection.Intervals) {
		interval := c.selection.Intervals[c.position]
		lo, hi := max(start, interval.Start), min(end, interval.End)
		if child.Position+start < lo {
			child.Position = lo - start
		}
		if child.Position+start < hi {
			c.crossEmpty(child, child.Position+start)
			return child.NextOccurrence(ctx)
		}
		c.position++
	}
	c.crossEmpty(child, child.Source.Count())
	return 0, false, ctx.Err()
}

func (c *queryBranchCursor) crossEmpty(child *Stage, ordinal int) {
	for c.emptyPosition < len(c.selection.EmptySegments) {
		index := c.selection.EmptySegments[c.emptyPosition]
		start := 0
		if index > 0 { start = child.Source.InheritanceEnds[index-1] }
		if start > ordinal { break }
		if child.scan != nil && (!child.scanRange || start >= child.scanStart && start <= child.scanEnd) {
			child.scan.CrossEmpty(index)
		}
		c.emptyPosition++
	}
}
