package catalog

import "testing"

func TestRoutineCostEstimate(t *testing.T) {
	for _, tc := range []struct {
		language RoutineLanguage
		cost, want float32
	}{
		{InternalRoutineLanguage, 0, 1},
		{CRoutineLanguage, 0, 1},
		{SQLRoutineLanguage, 0, 100},
		{PLpgSQLRoutineLanguage, 0, 100},
		{GoRoutineLanguage, 0, 1},
		{SQLRoutineLanguage, 1, 1},
		{SQLRoutineLanguage, 3.25, 3.25},
		{CRoutineLanguage, 100, 100},
	} {
		d := Procedure{Display: RoutineDisplay{Language: tc.language}, Cost: tc.cost}
		if got := d.CostEstimate(); got != tc.want {
			t.Fatalf("language %v cost %v: got %v, want %v", tc.language, tc.cost, got, tc.want)
		}
	}
}
