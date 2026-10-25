package analyze

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func TestAggregateMovingSelection(t *testing.T) {
	for _, tc := range []struct {
		name             string
		inverse          ids.OID
		ordinary, moving string
		start            parser.FrameBoundKind
		argument         expr.Expr
		want             bool
	}{
		{"no_inverse", 0, "w", "r", parser.FrameCurrentRow, nil, false},
		{"bounded", 1, "r", "r", parser.FrameOffsetPreceding, nil, true},
		{"unbounded", 1, "r", "r", parser.FrameUnboundedPreceding, nil, false},
		{"volatile", 1, "r", "r", parser.FrameOffsetPreceding, plan.ParameterExpr{Info: expr.Info{Property: expr.Properties{Volatility: expr.Volatile}}}, false},
		{"subquery", 1, "r", "r", parser.FrameOffsetPreceding, plan.SubqueryExpr{}, false},
		{"forced_unbounded", 1, "s", "r", parser.FrameUnboundedPreceding, nil, true},
		{"forced_volatile", 1, "w", "r", parser.FrameUnboundedPreceding, plan.ParameterExpr{Info: expr.Info{Property: expr.Properties{Volatility: expr.Volatile}}}, true},
		{"forced_subquery", 1, "s", "r", parser.FrameUnboundedPreceding, plan.SubqueryExpr{}, true},
		{"unsafe_moving_selected", 1, "r", "w", parser.FrameOffsetPreceding, nil, true},
		{"unsafe_moving_excluded", 1, "r", "s", parser.FrameUnboundedPreceding, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := catalog.AggregateDefinition{Functions: [8]ids.OID{0, 0, 0, 0, 0, 0, tc.inverse}, FinalModify: tc.ordinary, MovingFinalModify: tc.moving}
			frame := plan.WindowFrame{Start: plan.WindowBound{Kind: tc.start}}
			var args []expr.Expr
			if tc.argument != nil {
				args = []expr.Expr{tc.argument}
			}
			if got := MovingAggregateSelected(d, frame, args); got != tc.want {
				t.Fatal("selected moving", got, "want", tc.want)
			}
		})
	}
}
