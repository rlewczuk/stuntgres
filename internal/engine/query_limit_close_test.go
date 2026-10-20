package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLimitRequiredClose(t *testing.T) {
	for _, mode := range []string{"all", "one", "ties"} {
		for _, primary := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/close veto", true: "/primary preserved"}[primary], func(t *testing.T) {
				e, query, input := queryCoreFixture(t, "SELECT id FROM t")
				project := query.Root.(*plan.ProjectNode)
				cleanup := errors.New("required inner close")
				failure := errors.New("primary projection failure")
				closes := 0
				inner := indexedStage{Source: input, Columns: project.Schema(), Expressions: project.Expressions, Evaluation: EvalContext{EvalFrame: evalFrame{Scope: 1, Frame: 1}}, Finish: func() error { closes++; return cleanup }}
				if primary {
					inner.Expressions = []expr.Expr{queryProbe(project.Schema()[0].Usage, func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, failure })}
				}
				bridge := &querySource{Ref: input.Ref, NestedRoot: project, NestedInputs: input, NestedStage: &inner, Materialized: true, FlatWidth: 1}
				var child plan.Node = &plan.SourceNode{QuerySchema: project.Schema(), Ref: input.Ref}
				if mode != "all" {
					limit := &plan.LimitNode{QuerySchema: project.Schema(), Child: child, Count: queryLimitBound(t, e, 1)}
					if mode == "ties" {
						comparison, err := sqltype.BindOrdering(project.Schema()[0].Usage)
						if err != nil {
							t.Fatal(err)
						}
						limit.WithTies = true
						limit.PeerKeys = []plan.SortKey{{Slot: 0, Cache: -1, Comparison: comparison}}
					}
					child = limit
				}
				outer := &plan.ProjectNode{QuerySchema: project.Schema(), Child: child, Expressions: project.Expressions}
				rows, n, err := exec.Finalize(context.Background(), outer, bridge, nil)
				want := cleanup
				if primary {
					want = failure
				}
				if rows != nil || n != 0 || err != want || closes != 1 || input.View != nil || bridge.NestedStage != nil {
					t.Fatal(rows, n, err, want, closes, input.View, bridge.NestedStage)
				}
			})
		}
	}
}

func TestQueryLimitEOFCloses(t *testing.T) {
	_, query, input := queryCoreFixture(t, "SELECT id FROM t")
	project := query.Root.(*plan.ProjectNode)
	failure := errors.New("EOF close failure")
	closes := 0
	child := indexedStage{Source: input, Columns: project.Child.Schema(), Selection: []int{0}, Finish: func() error { closes++; return failure }}
	stage := exec.ProducingStage(&child, project.Schema())
	stage.Expressions = project.Expressions
	stage.Evaluation = EvalContext{EvalFrame: evalFrame{Scope: 1, Frame: 1}}
	stage.Produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		i, present, err := child.NextOccurrence(ctx)
		if err != nil || !present {
			return present, err
		}
		exec.RetainOccurrence(out, &child, i)
		return true, nil
	}
	values := make([]sqltype.Value, 1)
	if present, err := stage.Next(context.Background(), values); !present || err != nil {
		t.Fatal(present, err)
	}
	if present, err := stage.Next(context.Background(), values); present || err != failure {
		t.Fatal(present, err)
	}
	if err := stage.Close(); err != nil || closes != 1 || stage.Source != nil || stage.Produce != nil || stage.Finish != nil {
		t.Fatal(err, closes, stage)
	}
}
