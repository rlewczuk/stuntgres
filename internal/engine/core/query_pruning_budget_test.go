package core_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

// Counting the existing cancellation checkpoints observes actual proof traversal,
// without a timer, a synthetic cancellation, or a production instrumentation seam.
type pruningWorkContext struct {
	context.Context
	checks int
}

func (c *pruningWorkContext) Err() error {
	c.checks++
	return c.Context.Err()
}

func TestPartitionPruningSearchBound(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			e := engine.New()
			columns := []string{"k INT"}
			predicates := make([]string, 12)
			zeros := make([]string, 12)
			for i := range predicates {
				columns = append(columns, fmt.Sprintf("a%d INT", i))
				predicates[i] = fmt.Sprintf("(a%d=0 OR a%d=1)", i, i)
				zeros[i] = "0"
			}
			enginetest.Run(t, e, "CREATE TABLE pp_budget("+strings.Join(columns, ",")+") PARTITION BY RANGE(k)")
			for i := range 8 {
				enginetest.Run(t, e, fmt.Sprintf("CREATE TABLE pp_budget_%d PARTITION OF pp_budget FOR VALUES FROM(%d) TO(%d)", i, 1+2*i, 3+2*i))
				if populated {
					enginetest.Run(t, e, fmt.Sprintf("INSERT INTO pp_budget VALUES(%d,%s)", 1+2*i, strings.Join(zeros, ",")))
				}
			}
			query := "SELECT k FROM pp_budget WHERE " + strings.Join(predicates, " AND ") + " AND k=99"
			bound, source, release := hashImplicitQuery(t, e, query)
			defer release()
			project := bound.Root.(*plan.ProjectNode)
			filter := project.Child.(*plan.FilterNode)
			if source.BranchPruner == nil {
				t.Fatal("missing captured branch proof authority")
			}
			// Reusing the captured source must still give each Select fresh work.
			for attempt := range 2 {
				ctx := &pruningWorkContext{Context: context.Background()}
				state := &exec.State{Source: source, Frame: project.Frame}
				evaluation := state.Evaluation(ctx)
				if scope := engine.ScopeOf(source.LiveHost()); scope != nil {
					evaluation.Input = scope.Input
				}
				selected, err := source.BranchPruner.Select(ctx, &evaluation, filter.Predicate)
				if err != nil {
					t.Fatal(err)
				}
				// Err checkpoints are not search entries. Each processed entry
				// checks once; constrain makes at most three ordering calls, each
				// checking twice on this fixture's scalar INT4 provider path
				// (IndexComparison.Compare and Comparison.Compare). Restoration
				// adds no checks. Membership contradicts k=99 before finish.
				// Outside those entries: 49 normalized expression nodes, eight
				// branch entries, Select start/end, and one exhausted search entry.
				// This bounds the WHOLE Select, not each physical branch. The old
				// DFS exceeds it; no timing or synthetic cancellation is involved.
				const checkpointCeiling = 7*4096 + 49 + 8 + 2 + 1
				if ctx.checks > checkpointCeiling {
					t.Errorf("attempt %d: proof work exceeded whole-Select ceiling: %d checkpoints > %d", attempt, ctx.checks, checkpointCeiling)
				}
				if ctx.checks < 4096 {
					t.Errorf("attempt %d: Select did not receive a fresh search allowance: only %d checkpoints", attempt, ctx.checks)
				}
				if selected.Applied || len(selected.Intervals) != 0 {
					t.Errorf("attempt %d: exhausted proof must conservatively retain branches: %+v", attempt, selected)
				}
			}
		})
	}
}
