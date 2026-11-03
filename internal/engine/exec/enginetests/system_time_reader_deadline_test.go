package enginetests_test

import (
	"context"
	"errors"
	"math"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

// Each phase is entered by a real bound SQL subquery, during RLS traversal.
// The observer gates a real support/cursor/checkpoint; the support then returns
// normally. It does not inject an executor error or replace an algorithm.
func TestSystemTimeNestedReaderDeadline(t *testing.T) {
	for _, phase := range []struct {
		name, scalar, ancestor, caller string
		gate, exhaustive               int
	}{
		{"rows_reservoir", "SELECT bool_and(f) FROM t TABLESAMPLE system_rows(1)", "stuntgres/internal/engine/exec.selectSampleRows", "stuntgres/internal/engine/exec.selectSampleRows", 3, 9},
		// Bound the genuine inner scan before ORDER BY: sorting the shared
		// 64-row population can exhaust the clock before reaching WITH TIES.
		// Both retained rows have id > 0, so the first peer key is equal and
		// only the second (distinct id) can reject the candidate in Infinity.
		{"ties_keys", "SELECT bool_and(f) FROM (SELECT f FROM (SELECT id,f FROM t LIMIT 2) bounded ORDER BY id > 0, id FETCH FIRST 1 ROW WITH TIES) s", "stuntgres/internal/engine/exec.QueryTiesPeer", "stuntgres/internal/sqltype.Comparison.Compare", 1, 2},
		{"from_cursors", "SELECT bool_and(a > 0) FROM ROWS FROM (generate_series(1,3), generate_series(1,3)) s(a,b)", "stuntgres/internal/engine/exec.(*FunctionSetInvocation).NextFrom", "stuntgres/internal/engine/expr.(*SourceCursor).Next", 1, 8},
		{"target_cursors", "SELECT bool_and(a > 0) FROM (SELECT generate_series(1,3) a, generate_series(1,3) b FROM t LIMIT 3) s", "stuntgres/internal/engine/exec.OpenSetProjectNode.func2", "stuntgres/internal/engine/expr.(*SourceCursor).Next", 3, 6},
		{"recursive_keys", "WITH RECURSIVE r(n,k) AS (SELECT 1,1 FROM t UNION SELECT n,k FROM r WHERE false) SEARCH DEPTH FIRST BY n,k SET seq SELECT bool_and(n > 0) FROM r", "stuntgres/internal/engine/exec.(*recursiveUnionStage).FindTuple", "stuntgres/internal/sqltype.Grouping.Equal", 1, 2},
	} {
		t.Run(phase.name, func(t *testing.T) {
			for _, mode := range []string{"accepted_prefix", "expired_cancel", "infinity", "infinity_cancel"} {
				timeRunDeadline(t, mode, func(t *testing.T) {
					base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					ctx := &timeLoopContext{Context: base, ancestor: phase.ancestor}
					f := timeNewLoopFixture(t, ctx, "SELECT *, ("+phase.scalar+") FROM t TABLESAMPLE system_time(100)")
					if phase.name == "rows_reservoir" {
						// Opening the plain producer does not produce its rows.
						// Bound both actual sources before any capture demand,
						// keeping genuine quartet rows and the original owner.
						if f.input.FlatCount != 0 || len(f.input.Flat) != 0 || f.producer.Source != f.inputRaw || f.producer.Selection != nil {
							t.Fatal("reservoir fixture already produced or replaced its capture")
						}
						f.rows = f.rows[:4]
						f.raw.Anonymous, f.inputRaw.Anonymous = f.rows, f.rows
					}
					policy := f.project.Expressions[3].(plan.SubqueryExpr)
					f.install(policy, true)
					defer f.program.Close()
					calls := 0
					var supportEntry uintptr
					ctx.observeStack = func(stack []runtime.Frame, entered bool) {
						if !entered || len(stack) == 0 || stack[0].Function != phase.caller {
							return
						}
						// The two ordinary Err samples around one successful
						// Grouping.Equal are not two recursive key comparisons.
						if phase.name == "recursive_keys" && !timeSupportAdmission(stack[0], phase.caller, &supportEntry) {
							return
						}
						calls++
						if calls == phase.gate {
							_ = timeWorkPastBudget(base)
							if mode == "expired_cancel" || mode == "infinity_cancel" {
								cancel()
							}
						}
					}
					leaf := f.leaf
					if mode == "infinity" || mode == "infinity_cancel" {
						leaf = timeArgument(t, leaf, math.Inf(1))
					}
					stage, err := exec.OpenNode(ctx, &exec.State{Source: f.raw}, leaf)
					defer stage.Close()
					if calls < phase.gate {
						t.Error("bound SQL missed its real reader phase", phase.name, calls)
					}
					if mode == "expired_cancel" || mode == "infinity_cancel" {
						if !errors.Is(err, context.Canceled) || stage.Source != nil || stage.Selection != nil {
							t.Error("reader cancellation became successful elapsed EOF", err)
						}
					} else {
						if err != nil {
							t.Fatal("reader elapsed EOF escaped as statement failure", err)
						}
						if mode == "infinity" {
							if calls < phase.exhaustive {
								t.Error("Infinity failed to exhaust real reader work", phase.name, calls, phase.exhaustive)
							} else if phase.name == "ties_keys" && calls != phase.exhaustive {
								t.Error("Infinity did not compare exactly the two real peer keys", calls, phase.exhaustive)
							} else if phase.name == "rows_reservoir" && calls != phase.exhaustive {
								// Entry, post-Drain, sizing group, reservoir group,
								// four population visits, and final checkpoint.
								t.Error("Infinity did not exhaust exactly the real quartet reservoir", calls, phase.exhaustive)
							}
						} else if phase.name == "rows_reservoir" {
							// An ordinary host Err call at the next checkpoint is
							// allowed; the population tail must remain unvisited.
							if calls >= phase.exhaustive {
								t.Error("nested SYSTEM_ROWS consumed post-Drain reservoir tail", calls)
							}
						} else if calls != phase.gate {
							t.Error("reader advanced to another support/cursor after real expiry", phase.name, calls, phase.gate)
						}
						// Infinity policies may admit later rows when their genuine
						// scalar is true; finite EOF must retain exactly the prefix.
						if mode != "infinity" {
							f.assertRetained(t, &stage, true)
						}
					}
					clause := &f.program.Groups[0].Permissive[0]
					if clause.Evaluation.Context != nil || clause.Evaluation.Query != nil {
						t.Error("reader retained invocation scratch")
					}
					f.close(t, &stage)
				})
			}
		})
	}
}
