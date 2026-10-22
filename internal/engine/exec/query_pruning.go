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
}

// QueryBranchPruner is optional captured metadata authority. Selection is made
// unlocked, once per execution/filter opening, and never belongs to a held plan.
type QueryBranchPruner interface {
	Select(context.Context, *Context, expr.Expr) (QueryBranchSelection, error)
}

type queryBranchCursor struct {
	selection QueryBranchSelection
	position  int
}

func selectQueryBranches(ctx context.Context, state *State, filter *plan.FilterNode, child *Stage) (queryBranchCursor, error) {
	if _, ok := filter.Child.(*plan.SourceNode); !ok || child.Produce != nil || child.Source.BranchPruner == nil || child.Source.RlsDenied || QueryHasMandatoryPhysicalFrontier(filter.Child, state.Source) {
		return queryBranchCursor{}, nil
	}
	evaluation := state.Evaluation(ctx)
	selection, err := child.Source.BranchPruner.Select(ctx, &evaluation, filter.Predicate)
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
			return child.NextOccurrence(ctx)
		}
		c.position++
	}
	return 0, false, ctx.Err()
}
