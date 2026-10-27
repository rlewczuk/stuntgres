package enginetests

import (
	"math"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

func TestPlannerCostsModel(t *testing.T) {
	index:=ids.ObjectAddress{ObjectID:42}
	for _,tc:=range []struct{rows,entry,full,hash float64; method plan.ScanMethod}{
		{0,.005,0,.0025,plan.FullScan},{1,.005,.0125,.0125,plan.FullScan},
		{5,.005,.0625,.0525,plan.HashCandidates},{1000,.005,12.5,10.0025,plan.HashCandidates},
		{5,1000000,.0625,2500000.04,plan.FullScan},{1000,1000000,12.5,500000007.5025,plan.FullScan},
	} {
		c:=plan.DefaultPlannerCosts(); c.IndexTuple=tc.entry
		d:=c.Scan(tc.rows,index,"h")
		if math.Abs(d.FullCost-tc.full)>1e-8 || math.Abs(d.HashCost-tc.hash)>1e-7 || d.Method!=tc.method { t.Fatal(tc,d) }
	}
	for _,tc:=range []struct{costs plan.PlannerCosts; method plan.ScanMethod}{
		{plan.PlannerCosts{},plan.FullScan},
		{plan.PlannerCosts{Tuple:1},plan.HashCandidates},
		{plan.PlannerCosts{IndexTuple:1},plan.FullScan},
		{plan.PlannerCosts{Operator:1},plan.FullScan},
		{plan.PlannerCosts{Tuple:math.MaxFloat64,IndexTuple:math.MaxFloat64/2,Operator:math.MaxFloat64/4},plan.HashCandidates},
		{plan.PlannerCosts{Tuple:math.MaxFloat64/4,IndexTuple:math.MaxFloat64,Operator:math.MaxFloat64/4},plan.FullScan},
	} {
		d:=tc.costs.Scan(1000,index,"h")
		if d.Method!=tc.method || math.IsNaN(d.FullCost) || math.IsInf(d.FullCost,0) || math.IsNaN(d.HashCost) || math.IsInf(d.HashCost,0) { t.Fatal(tc,d) }
		if d:=tc.costs.Scan(1000,index,""); d.Method!=plan.FullScan { t.Fatal("ineligible selected hash",d) }
	}
	cheap:=plan.PlannerCosts{Tuple:.01,Operator:.0025}
	if !cheap.LocalParallel(1000,2,2) || cheap.LocalParallel(1000,2,1) || cheap.LocalParallel(0,2,2) { t.Fatal("worker eligibility") }
	for _,mutate:=range []func(*plan.PlannerCosts){
		func(c *plan.PlannerCosts){c.ParallelSetup=1000000},
		func(c *plan.PlannerCosts){c.ParallelTuple=1000000},
		func(c *plan.PlannerCosts){c.MinParallelTableBlocks=6}, // ceil(40000/8192)=5.
		func(c *plan.PlannerCosts){c.ParallelSetup=6.25}, // tie: 6.25+12.5/2.
	} { c:=cheap; mutate(&c); if c.LocalParallel(1000,2,2) { t.Fatal("expensive/tied parallel selected",c) } }
	cheap.MinParallelTableBlocks=5
	if !cheap.LocalParallel(1000,2,2) { t.Fatal("inclusive footprint floor") }
	huge:=plan.PlannerCosts{Tuple:math.MaxFloat64,Operator:math.MaxFloat64}
	if !huge.LocalParallel(1000,2,2) { t.Fatal("overflow manufactured tie") }
}
