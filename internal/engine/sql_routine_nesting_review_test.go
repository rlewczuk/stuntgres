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
		{"ordinary", argumentWindowOwner{active: true}, 1, true},
		{"window", argumentWindowOwner{active: true, window: true}, 1, true},
		{"scalar", argumentWindowOwner{}, 1, false},
		{"foreign frame", argumentWindowOwner{active: true}, 2, false},
		{"first occurrence", argumentWindowOwner{active: true, found: true, first: prior}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &plan.WindowExpr{Info: expr.Info{Pos: position}, Frame: tc.frame, Slot: -1, Call: -1}
			b := &expressionBinder{
				ctx:      context.Background(),
				query:    queryScopeState{frame: 1},
				clause:   clauseState{selectedOperatorArgs: []expr.Expr{node}},
				grouping: groupingState{argumentOwner: tc.owner},
			}
			bound, err := b.bind(parser.ParameterExpr{Number: -1}, expressionPolicy{context: projectionContext})
			if err != nil || bound != node {
				t.Fatalf("known argument was replaced or evaluated: %v, %v", bound, err)
			}
			want := tc.owner
			if tc.found && !want.found {
				want.first, want.found = position, true
			}
			if b.grouping.argumentOwner != want {
				t.Fatalf("owner = %+v, want %+v", b.grouping.argumentOwner, want)
			}
			// Reusing the same carrier must not change occurrence identity,
			// register a second window, or replace the owner's first position.
			bound, err = b.bind(parser.ParameterExpr{Number: -1}, expressionPolicy{context: projectionContext})
			if err != nil || bound != node || b.grouping.argumentOwner != want || len(b.grouping.windowCalls) != 0 || len(b.grouping.aggregates) != 0 {
				t.Fatal("carrier reuse changed bound identity or owner/registration state", err)
			}
		})
	}
}
