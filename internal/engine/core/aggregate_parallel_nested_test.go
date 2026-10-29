package core_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Compose the real string state/codec and physical-source admission seam with
// lexical invocation identity. The SQL ownership matrix separately reaches this
// marker through real worker, leader and serial SQL nonterminal SELECTs.
func aggregateParallelNestedAdmission(t *testing.T) {
	for _, phase := range []string{"serial", "leader", "worker"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source, _, probe := aggregateCombineParallelFixture(t)
			source.PlannerCosts = plan.PlannerCosts{Tuple: .01, IndexTuple: .005, Operator: .0025}
			source.PlannerRows, source.PlannerColumns = 1000, 3
			fresh, err := exec.CaptureSource(context.Background(), source.View, source.Ref)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			fresh.PlannerCosts, fresh.PlannerRows, fresh.PlannerColumns = source.PlannerCosts, source.PlannerRows, source.PlannerColumns
			if _, ok := exec.ParallelAggregatePlan(query.Root, source); !ok || exec.QueryParallelWorkers(2, source.Count()) != 2 || !source.PlannerCosts.LocalParallel(source.PlannerRows, source.PlannerColumns, 2) {
				t.Fatal("nested fixture would not otherwise admit real partial work")
			}
			ctx := context.Background()
			if phase == "leader" {
				ctx = exec.ParallelOperationContext(ctx)
			}
			if phase == "worker" {
				ctx = exec.ParallelWorkerContext(ctx)
			}
			ctx = exec.SQLInvocationContext(ctx)
			var inputs atomic.Int32
			probe.advance = func(ctx context.Context, _ any, _ []sqltype.Value, _ expr.Context) error {
				if !exec.WithinSQLInvocation(ctx) || exec.InParallelWorker(ctx) != (phase == "worker") || exec.InParallelOperation(ctx) != (phase != "serial") {
					return fmt.Errorf("nested SELECT changed lexical/effect/worker identity")
				}
				inputs.Add(1)
				return nil
			}
			rows, count, err := exec.FinalizeSelect(ctx, query.Root, source, nil, 2)
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			if got := aggregateCombineString(t, rows); got != "1,2,3,4" {
				t.Fatal(got)
			}
			if inputs.Load() != 4 || probe.encodes.Load() != 0 || probe.decodes.Load() != 0 || probe.combines.Load() != 0 || probe.finals.Load() != 1 {
				t.Fatal("nested lexical owner launched partial states", inputs.Load(), probe.encodes.Load(), probe.decodes.Load(), probe.combines.Load(), probe.finals.Load())
			}
			// The lexical exclusion is not a persistent downgrade. The same graph
			// next runs with real independent partial states and opaque transport.
			probe.advance = func(ctx context.Context, _ any, _ []sqltype.Value, _ expr.Context) error {
				if !exec.InParallelWorker(ctx) || !exec.InParallelOperation(ctx) || exec.WithinSQLInvocation(ctx) {
					return fmt.Errorf("ordinary outer parallel admission retained lexical owner")
				}
				return nil
			}
			rows, count, err = exec.FinalizeSelect(context.Background(), query.Root, fresh, nil, 2)
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			if got := aggregateCombineString(t, rows); got != "1,2,3,4" {
				t.Fatal(got)
			}
			if probe.encodes.Load() != 2 || probe.decodes.Load() != 2 || probe.combines.Load() != 2 || probe.finals.Load() != 2 {
				t.Fatal("outer parallel reuse lost real state transport", probe.encodes.Load(), probe.decodes.Load(), probe.combines.Load(), probe.finals.Load())
			}
		})
	}
}
