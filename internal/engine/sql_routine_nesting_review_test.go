package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func TestSQLRoutineKnownArgumentWindowOwner(t *testing.T) {
	position := parser.Position{Character: 27}
	prior := parser.Position{Character: 12}
	for _, tc := range []struct {
		name  string
		owner argumentWindowOwner
		frame uint32
		found bool
	}{
		{"ordinary", argumentWindowOwner{Active: true}, 1, true},
		{"window", argumentWindowOwner{Active: true, Window: true}, 1, true},
		{"scalar", argumentWindowOwner{}, 1, false},
		{"foreign frame", argumentWindowOwner{Active: true}, 2, false},
		{"first occurrence", argumentWindowOwner{Active: true, Found: true, First: prior}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &plan.WindowExpr{Info: expr.Info{Pos: position}, Frame: tc.frame, Slot: -1, Call: -1}
			b := &expressionBinder{
				Ctx:      context.Background(),
				Query:    queryScopeState{Frame: 1},
				Clause:   clauseState{SelectedOperatorArgs: []expr.Expr{node}},
				Grouping: groupingState{ArgumentOwner: tc.owner},
			}
			bound, err := b.Bind(parser.ParameterExpr{Number: -1}, expressionPolicy{Context: ProjectionContext})
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
			bound, err = b.Bind(parser.ParameterExpr{Number: -1}, expressionPolicy{Context: ProjectionContext})
			if err != nil || bound != node || b.Grouping.ArgumentOwner != want || len(b.Grouping.WindowCalls) != 0 || len(b.Grouping.Aggregates) != 0 {
				t.Fatal("carrier reuse changed bound identity or owner/registration state", err)
			}
		})
	}
}
