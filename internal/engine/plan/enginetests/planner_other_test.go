package enginetests

import (
	"math"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

func TestPlannerOtherCursorScanScores(t *testing.T) {
	index := ids.ObjectAddress{ObjectID: 42}
	costs := plan.DefaultPlannerCosts()
	for _, tc := range []struct {
		fraction, full, hash float64
		method               plan.ScanMethod
	}{{1, 12.5, 10.0025, plan.HashCandidates}, {.0001, .00125, .0035, plan.FullScan}, {0, 12.5e-10, .002500001, plan.FullScan}} {
		d := costs.Scan(1000, tc.fraction, index, "h")
		if d.Method != tc.method || math.Abs(d.FullScore-tc.full) > 1e-12 || math.Abs(d.HashScore-tc.hash) > 1e-12 || d.FullStartup != 0 || d.HashStartup != .0025 || d.FullCost != 12.5 || math.Abs(d.HashCost-10.0025) > 1e-12 {
			t.Fatal(tc, d)
		}
	}
	if d := (plan.PlannerCosts{}).Scan(1000, 0, index, "h"); d.Method != plan.FullScan || d.FullScore != 0 || d.HashScore != 0 {
		t.Fatal("zero cost tie", d)
	}
	for _, fraction := range []float64{0, .0001, 1} {
		huge := plan.PlannerCosts{Tuple: math.MaxFloat64, IndexTuple: math.MaxFloat64 / 2, Operator: math.MaxFloat64 / 4}
		d := huge.Scan(1000, fraction, index, "h")
		for _, n := range []float64{d.FullCost, d.HashCost, d.FullStartup, d.HashStartup, d.FullScore, d.HashScore} {
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > plan.ExplainEstimateLimit {
				t.Fatal(d)
			}
		}
		if fraction == 1 && d.Method != plan.HashCandidates || fraction == .0001 && d.Method != plan.FullScan {
			t.Fatal("display saturation changed normalized choice", fraction, d)
		}
	}
}
