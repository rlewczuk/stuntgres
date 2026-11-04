package enginetests_test

import (
	"context"
	"errors"
	"math"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func TestSystemTimeNestedJoinDeadline(t *testing.T) {
	for _, mode := range []string{"accepted_prefix", "expired_cancel", "infinity", "infinity_cancel"} {
		timeRunDeadline(t, mode, func(t *testing.T) {
			base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx := &timeLoopContext{Context: base, ancestor: "stuntgres/internal/engine/exec.(*joinHash).build"}
			// The first left row genuinely exhausts and caches the right input
			// before joinHash.build starts. Bound both SQL inputs to two rows
			// so pre-build production/predicates plus stack observation do not
			// spend the 100ms budget before the first real hash admission.
			f := timeNewLoopFixture(t, ctx, "SELECT *, (SELECT bool_and(l.f) FROM (SELECT id,v,f FROM t LIMIT 2) l JOIN (SELECT id,v FROM t LIMIT 2) r ON l.id = r.id AND l.v = r.v) FROM t TABLESAMPLE system_time(100)")
			policy := f.project.Expressions[3].(plan.SubqueryExpr)
			joins := 0
			plan.VisitExecutableQuery(policy.QueryPlan.Root, func(node plan.Node) {
				if n, ok := node.(*plan.JoinNode); ok {
					if !exec.HashJoinEligible(n, n.Frame) {
						t.Fatal("bound two-key join is not eligible for its real hash build")
					}
					joins++
				}
			}, func(expr.Expr) {})
			if joins != 1 {
				t.Fatal("fixture lost its actual hash join", joins)
			}
			f.install(policy, true)
			defer f.program.Close()
			hashes, laterKeys := 0, 0
			armed := false
			var hashEntry uintptr
			ctx.observeStack = func(stack []runtime.Frame, building bool) {
				if !building || len(stack) == 0 {
					return
				}
				caller := stack[0]
				if armed && caller.Function == "stuntgres/internal/engine/exec.EvaluationContext" {
					// A direct joinHash.key EvaluationContext is the required
					// post-success checkpoint, not another key evaluation.
					// Only an actual bound column evaluator admits that work.
					for _, frame := range stack[1:] {
						if frame.Function == "stuntgres/internal/engine/exec.(*joinHash).key" {
							break
						}
						if frame.Function == "stuntgres/internal/engine/plan.ColumnExpr.Eval" {
							laterKeys++
							break
						}
					}
				}
				if timeSupportAdmission(caller, "stuntgres/internal/sqltype.Grouping.HashWithBoundary", &hashEntry) {
					hashes++
					if hashes == 1 {
						// Hash receives a genuine already-evaluated first key.
						// It succeeds after the elapsed gate unless host-canceled.
						_ = timeWorkPastBudget(base)
						armed = true
						if mode == "expired_cancel" || mode == "infinity_cancel" {
							cancel()
						}
					}
				}
			}
			leaf := f.leaf
			if mode == "infinity" || mode == "infinity_cancel" {
				leaf = timeArgument(t, leaf, math.Inf(1))
			}
			stage, err := exec.OpenNode(ctx, &exec.State{Source: f.raw}, leaf)
			defer stage.Close()
			if !armed {
				t.Error("fixture missed genuine cached-right hash-build support")
			}
			if mode == "expired_cancel" || mode == "infinity_cancel" {
				if !errors.Is(err, context.Canceled) || stage.Source != nil || stage.Selection != nil {
					t.Error("hash support cancellation became elapsed EOF", err)
				}
			} else {
				if err != nil {
					t.Fatal("nested hash private unwind escaped as statement error", err)
				}
				if mode == "infinity" {
					// QueryFixture's first two rows have keys (1,1) and
					// (2,NULL). All four columns are evaluated, but the NULL
					// short-circuits before Hash: three hash admissions and
					// three real key evaluators after the first hash.
					if hashes != 3 || laterKeys != 3 {
						t.Error("Infinity failed to exhaust the two-row two-key right-side hash build", hashes, laterKeys)
					}
				} else {
					if hashes != 1 || laterKeys != 0 {
						t.Error("hash build advanced to another key after successful expired support", hashes, laterKeys)
					}
					f.assertRetained(t, &stage, true)
				}
			}
			clause := &f.program.Groups[0].Permissive[0]
			if clause.Evaluation.Context != nil || clause.Evaluation.Query != nil {
				t.Error("hash unwind retained RLS invocation scratch")
			}
			f.close(t, &stage)
		})
	}
}
