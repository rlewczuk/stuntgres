package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func TestSQLRoutineKnownArgumentWindowOwner(t *testing.T) {
	position := parser.Position{Character: 27}
	prior := parser.Position{Character: 12}
	for _, tc := range []struct {
		name  string
		owner analyze.ArgumentWindowOwner
		frame uint32
		found bool
	}{
		{"ordinary", analyze.ArgumentWindowOwner{Active: true}, 1, true},
		{"window", analyze.ArgumentWindowOwner{Active: true, Window: true}, 1, true},
		{"scalar", analyze.ArgumentWindowOwner{}, 1, false},
		{"foreign frame", analyze.ArgumentWindowOwner{Active: true}, 2, false},
		{"first occurrence", analyze.ArgumentWindowOwner{Active: true, Found: true, First: prior}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &plan.WindowExpr{Info: expr.Info{Pos: position}, Frame: tc.frame, Slot: -1, Call: -1}
			b := &analyze.Binder{
				Ctx:      context.Background(),
				Query:    analyze.QueryScopeState{Frame: 1},
				Clause:   analyze.ClauseState{SelectedOperatorArgs: []expr.Expr{node}},
				Grouping: analyze.GroupingState{ArgumentOwner: tc.owner},
			}
			bound, err := b.Bind(parser.ParameterExpr{Number: -1}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil || bound != node {
				t.Fatalf("known argument was replaced or evaluated: %v, %v", bound, err)
			}
			want := tc.owner
			if tc.found && !want.Found {
				want.First, want.Found = position, true
			}
			if b.Grouping.ArgumentOwner != want {
				t.Fatalf("owner = %+v, want %+v", b.Grouping.ArgumentOwner, want)
			}
			// Reusing the same carrier must not change occurrence identity,
			// register a second window, or replace the owner's first position.
			bound, err = b.Bind(parser.ParameterExpr{Number: -1}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil || bound != node || b.Grouping.ArgumentOwner != want || len(b.Grouping.WindowCalls) != 0 || len(b.Grouping.Aggregates) != 0 {
				t.Fatal("carrier reuse changed bound identity or owner/registration state", err)
			}
		})
	}
}
