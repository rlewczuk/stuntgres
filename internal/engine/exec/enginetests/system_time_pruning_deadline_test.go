package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Pruning is demanded by an unsampled scalar inside the sampled source's RLS,
// not by initial admission or by a directly sampled filter (which disables it).
// The topology, membership bounds, row prefixes and pruner are all captured by
// actual SQL admission; no interval vector or replacement pruner is supplied.
func TestSystemTimeNestedPruningDeadline(t *testing.T) {
	for _, mode := range []string{"accepted_prefix", "expired_cancel", "infinity", "infinity_cancel"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
			enginetest.Run(t, e, "CREATE TABLE time_prune(k INT) PARTITION BY RANGE(k)")
			const leaves = 16
			for i := range leaves {
				enginetest.Run(t, e, fmt.Sprintf("CREATE TABLE time_prune_%d PARTITION OF time_prune FOR VALUES FROM(%d) TO(%d)", i, 1+2*i, 3+2*i))
				enginetest.Run(t, e, fmt.Sprintf("INSERT INTO time_prune VALUES(%d)", 1+2*i))
			}
			query, captured := enginetest.QueryFixture(t, e, "SELECT *, (SELECT k = 1 FROM time_prune WHERE k = 1) FROM t TABLESAMPLE system_time(100)")
			project := query.Root.(*plan.ProjectNode)
			leaf := project.Child.(*plan.SourceNode)
			base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Keep admission alive through retained-capture teardown. Only the
			// logical execution below is canceled, as HashCaptureQuery requires.
			op, done, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			op.ConstraintExclusion = "on"
			innerQuery, input, release := enginetest.HashCaptureQuery(t, e, op, "SELECT k = 1 FROM time_prune WHERE k = 1")
			defer release()
			pruner, ok := input.BranchPruner.(*partition.CapturedPruner)
			if !ok || len(input.Inheritance) != leaves || input.Count() != leaves {
				t.Fatal("admission lost the actual populated partition topology/pruner", ok, len(input.Inheritance), input.Count())
			}
			view := input.View
			if view == nil {
				t.Fatal("partition capture has no retained snapshot authority")
			}
			prefixes := make([]*exec.Source, leaves)
			for i, segment := range input.Inheritance {
				prefixes[i] = segment.Prefix
				if segment.Prefix == nil || segment.Prefix.View == nil || input.InheritanceEnds[i] != i+1 {
					t.Fatal("partition capture did not retain real one-row physical prefixes", i)
				}
			}
			policy := project.Expressions[3].(plan.SubqueryExpr)
			// Use the independently admitted query's real frame/source identity,
			// retaining the original scalar operator and genuine EvalSubquery.
			policy.QueryPlan = innerQuery
			policyCalls := 0
			program := &exec.RLSProgram{Volatile: true, Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{
				State: exec.State{Source: input, SourceMode: exec.SourceBorrowed},
				Expression: enginetest.QueryProbe(policy.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					policyCalls++
					if policyCalls == 1 {
						return sqltype.Boolean(policy.ResultUsage().Type, true), nil
					}
					if policyCalls == 2 {
						return policy.Eval(c)
					}
					return sqltype.Boolean(policy.ResultUsage().Type, false), nil
				}),
			}}}}}
			defer program.Close()
			rows := make([][]sqltype.Value, captured.Count())
			for i := range rows {
				rows[i] = captured.Row(i)
			}
			raw := &exec.Source{Ref: captured.Ref, Anonymous: rows, RLS: program}
			ctx := &timeLoopContext{Context: base, ancestor: "stuntgres/internal/engine/partition.(*CapturedPruner).Select"}
			comparisons, branches, laterBranches := 0, 0, 0
			armed := false
			ctx.observe = func(caller string, pruning bool) {
				if !pruning {
					return
				}
				if caller == "stuntgres/internal/engine/partition.(*pruneProof).branch" {
					branches++
					if armed {
						laterBranches++
					}
				}
				if caller == "stuntgres/internal/sqltype.(*IndexComparison).Compare" {
					comparisons++
					if comparisons == 1 {
						// Real INT4 index support receives evaluated bound datums
						// and returns through nativeInt4Compare after this gate;
						// it does not call the generic Comparison.Compare path.
						_ = timeWorkPastBudget(base)
						armed = true
						if mode == "expired_cancel" || mode == "infinity_cancel" {
							cancel()
						}
					}
				}
			}
			if mode == "infinity" || mode == "infinity_cancel" {
				leaf = timeArgument(t, leaf, math.Inf(1))
			}
			stage, err := exec.OpenNode(ctx, &exec.State{Source: raw}, leaf)
			defer stage.Close()
			if !armed || comparisons == 0 || branches == 0 || policyCalls < 2 {
				t.Error("nested RLS missed real captured-topology pruning support", armed, comparisons, branches, policyCalls)
			}
			if mode == "expired_cancel" || mode == "infinity_cancel" {
				if !errors.Is(err, context.Canceled) || stage.Source != nil || stage.Selection != nil || laterBranches != 0 {
					t.Error("pruning cancellation became budget EOF or consumed topology tail", err, laterBranches)
				}
			} else {
				if err != nil {
					t.Fatal("pruning private elapsed unwind escaped as statement failure", err)
				}
				wantRows := 1
				if mode == "infinity" {
					wantRows = 2 // Exactly one real k=1 row makes the scalar TRUE.
					if branches != leaves || laterBranches != leaves-1 || comparisons < leaves {
						t.Error("Infinity failed to prove the complete real partition topology", branches, laterBranches, comparisons)
					}
				} else if comparisons != 1 || branches != 1 || laterBranches != 0 {
					t.Errorf("nested pruning consumed proof/topology tail after successful support expiry: comparisons=%d branches=%d later=%d of %d leaves", comparisons, branches, laterBranches, leaves)
				}
				if stage.Count() != wantRows || stage.Closed || stage.Terminal != nil || stage.Source != raw {
					t.Error("pruning EOF lost accepted source occurrences", stage.Count(), wantRows)
				}
				for i := range stage.Count() {
					if !reflect.DeepEqual(stage.Source.Row(stage.Index(i)), rows[i]) {
						t.Error("pruning EOF changed accepted source backing", i)
					}
				}
				if input.View != view || input.BranchPruner != pruner || len(input.Inheritance) != leaves {
					t.Error("pruning budget unwind released its borrowed snapshot/topology authority")
				} else {
					for i, segment := range input.Inheritance {
						if segment.Prefix != prefixes[i] || segment.Prefix.View == nil {
							t.Error("pruning budget unwind released a borrowed physical prefix", i)
						}
					}
				}
			}
			clause := &program.Groups[0].Permissive[0]
			if clause.Evaluation.Context != nil || clause.Evaluation.Query != nil || clause.State.Source != input {
				t.Error("pruning unwind retained RLS scratch or lost the capture owner")
			}
			if err := stage.Close(); err != nil || stage.Source != nil || stage.Selection != nil || stage.Finish != nil || stage.Reset != nil {
				t.Error("pruning sample close retained local state", err)
			}
			program.Close()
			program.Close()
			if input.View != nil || input.BranchPruner != nil {
				t.Error("pruning capture owner retained snapshot/pruner after idempotent close")
			}
			for i, prefix := range prefixes {
				if prefix.View != nil {
					t.Error("pruning capture owner retained a physical snapshot pin", i)
				}
			}
		})
	}
}
