package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestExplainAnalyzeProjectionTimingOwnership(t *testing.T) {
	for _, mode := range []string{"derived", "materialized-cte", "set-arm", "nested-projection", "error", "cancel", "timing-off", "observation-off"} {
		t.Run(mode, func(t *testing.T) {
			integer := queryLoopInteger(t)
			one, _ := sqltype.Integer(integer, 1)
			schema := plan.QuerySchema{{Name: "v", Usage: integer.Usage()}}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := base
			var observation *Observation
			if mode != "observation-off" { ctx, observation = Observe(base, mode != "timing-off") }
			project := &plan.ProjectNode{QuerySchema: schema, Child: &plan.ValuesNode{Rows: make([][]expr.Expr, 2)}, Frame: 2}
			calls := 0
			failure := errors.New("projection boundary failure")
			project.Expressions = []expr.Expr{transitionUnitExpr{Info: expr.Info{Usage: integer.Usage()}, run: func(c expr.Context) (sqltype.Value, error) {
				calls++
				if observation != nil {
					records := observation.records[project]
					if len(records) != 1 || records[0].depth == 0 { t.Fatal("inner original projection is not active", records) }
					r := records[0]
					if mode == "nested-projection" {
						started, elapsed, depth := r.started, r.elapsed, r.depth
						nested:=Stage{projection:r}
						nested.beginProjectionWork(); nested.endProjectionWork()
						if r.started != started || r.elapsed != elapsed || r.depth != depth { t.Fatal("nested same-node span double sampled") }
					}
				}
				if calls == 2 && mode == "error" { return sqltype.Value{}, failure }
				if calls == 2 && mode == "cancel" { cancel(); return sqltype.Value{}, context.Canceled }
				return one, nil
			}}}
			state := &State{Source: &Source{Materialized: true}}
			var stage Stage
			var arm SetArm
			var producer *CTEProducer
			var err error
			if mode == "materialized-cte" {
				owner, cteState, node := cteUnitScan(project.Expressions, schema, 2)
				defer owner.Close()
				project = node.Query.Root.(*plan.ProjectNode)
				stage, err = OpenCTEScan(ctx, cteState, node)
				producer = owner.Producers[node.CTE]
			} else if mode == "set-arm" {
				arm, err = openSetArm(ctx, state, project, project.Expressions, schema)
			} else {
				stage, err = OpenDerivedNode(ctx, state, &plan.DerivedNode{QuerySchema: schema, Query: &plan.Query{Root: project}})
			}
			if err != nil { t.Fatal(err) }
			closes:=0
			closing:=&stage
			if mode=="set-arm" {closing=&arm.Inner}
			finish:=closing.Finish
			closing.Finish=func()error {closes++;if finish!=nil {return finish()};return nil}
			for range 3 {
				var present bool
				if mode == "set-arm" { _, present, err = arm.Next(ctx) } else { _, present, err = stage.NextOccurrence(ctx) }
				if err != nil || !present { break }
			}
			if mode == "error" { if !errors.Is(err, failure) { t.Fatal(err) } } else if mode == "cancel" { if !errors.Is(err, context.Canceled) { t.Fatal(err) } } else if err != nil { t.Fatal(err) }
			if calls != 2 { t.Fatal("projection not evaluated exactly once", calls) }
			if producer != nil {
				for _, ordinal := range []int{0, 1, 0, 1} { if ok, err := producer.Ensure(ctx, ordinal); err != nil || !ok { t.Fatal(ok, err) } }
				if calls != 2 { t.Fatal("cached CTE replay reevaluated projection", calls) }
			}
			stage.Close(); arm.Close(); stage.Close(); arm.Close()
			if closes!=1 {t.Fatal("source owner closed more than once",closes)}
			if stage.Evaluation.Context != nil || arm.Inner.Evaluation.Context != nil { t.Fatal("evaluation context retained") }
			if observation != nil {
				for _, r := range observation.records[project] {
					if r.depth != 0 || r.loops != 1 || r.elapsed < 0 { t.Fatal("unsettled projection", r) }
					if mode == "timing-off" && (!r.started.IsZero() || r.elapsed != 0) { t.Fatal("TIMING OFF sampled clock") }
				}
			}
		})
	}
}
