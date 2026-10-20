package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONStripNullsJoinDependencies(t *testing.T) {
	// Four child-frame slots: left JSON/BOOL then right JSON/BOOL. Physical
	// origins are irrelevant: dependency masks follow executable formal slots.
	for _, tc := range []struct {
		name         string
		target, flag int
		nullTarget   bool
		want         uint8
	}{
		{"constants", -1, -1, false, 0},
		{"target_left", 0, -1, false, 1}, {"target_right", 2, -1, false, 2},
		{"flag_left", -1, 1, false, 1}, {"flag_right", -1, 3, false, 2},
		{"null_target_flag_left", -1, 1, true, 1}, {"null_target_flag_right", -1, 3, true, 2},
		{"same_left", 0, 1, false, 1}, {"same_right", 2, 3, false, 2},
		{"target_left_flag_right", 0, 3, false, 3}, {"target_right_flag_left", 2, 1, false, 3},
	} {
		for _, reordered := range []bool{false, true} {
			name := tc.name
			if reordered {
				name += "_reordered"
			}
			t.Run(name, func(t *testing.T) {
				b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
				q := `SELECT json_strip_nulls('{}',false)`
				if reordered {
					q = `SELECT json_strip_nulls(strip_in_arrays => false,target => '{}')`
				}
				e := jsonStripUnitBind(t, b, q).(functions.JSONStripNulls)
				if tc.nullTarget {
					e.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: e.Args[0].ResultUsage()}, Value: sqltype.Null(e.Args[0].ResultUsage().Type)}
				}
				for formal, slot := range [2]int{tc.target, tc.flag} {
					if slot < 0 {
						continue
					}
					e.Args[formal] = plan.ColumnExpr{Info: expr.Info{Usage: e.Args[formal].ResultUsage(), Property: expr.Properties{RowDependent: true}}, Slot: slot}
				}
				next, err := e.Reconstruct(e.Info, e.Args)
				if err != nil {
					t.Fatal(err)
				}
				sides, err := plan.JoinExpressionSides(next, 2, 2)
				if err != nil || sides != tc.want {
					t.Fatalf("dependency mask=%d error=%v want=%d", sides, err, tc.want)
				}
				if reordered && (next.Labels != [2]string{"strip_in_arrays", "target"} || next.WrittenSlots != [2]uint8{1, 0}) {
					t.Fatal("written mapping lost", next)
				}
			})
		}
	}
}
