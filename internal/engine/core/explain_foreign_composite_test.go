package core_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

type fdwExplainCompositeProbe struct {
	fdwUnitWrapper
	paths []catalog.ForeignQueryPath
	request catalog.ForeignQueryRequest
	failure error
	invalid string
	baseCalls, begins int
}

func (w *fdwExplainCompositeProbe) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	w.baseCalls++
	return catalog.ForeignRelSize{}, errors.New("base planning must not run")
}
func (w *fdwExplainCompositeProbe) GetForeignJoinPaths(_ context.Context, r catalog.ForeignQueryRequest) ([]catalog.ForeignQueryPath, error) {
	w.request = r
	compatibility := catalog.ForeignQueryCompatibility{Expressions:true, Collations:true, StatementVisibility:true}
	w.paths = nil
	for i, cost := range []float64{50,2,2,1} {
		w.paths = append(w.paths, catalog.ForeignQueryPath{TotalCost: cost, Compatibility: compatibility, Plan: catalog.ForeignQueryPlan{Slots:slices.Clone(r.Stage.Slots), Private:i}})
	}
	w.paths[3].Compatibility.Expressions = false
	switch w.invalid {
	case "cost": w.paths[3].TotalCost = math.NaN()
	case "layout": w.paths[3].Plan.Slots = nil
	case "decline": return nil, nil
	case "incompatible": for i := range w.paths { w.paths[i].Compatibility.Expressions = false }
	}
	// Mutating callback-owned translation must not alter the retained request.
	r.Stage.Slots[0].TypeName = "provider mutation"
	r.Sources[0].Request.TableOptions[0].Value = "provider mutation"
	return w.paths, w.failure
}
func (w *fdwExplainCompositeProbe) GetForeignUpperPaths(ctx context.Context, r catalog.ForeignQueryRequest) ([]catalog.ForeignQueryPath, error) { return w.GetForeignJoinPaths(ctx,r) }
func (w *fdwExplainCompositeProbe) BeginForeignQueryScan(context.Context, catalog.ForeignQueryRequest, catalog.ForeignQueryPlan) (catalog.ForeignScan, error) {
	w.begins++
	return nil, errors.New("planning started execution")
}

func TestFDWExplainCompositeSelectionOwnership(t *testing.T) {
	w := &fdwExplainCompositeProbe{}
	owner := new(int)
	occurrences := make(map[int]exec.ForeignPlanningOccurrence)
	input := exec.ForeignPlanningInput{Frame:1, Occurrence:func(i int) exec.ForeignPlanningOccurrence { return occurrences[i] }}
	for i := range 2 {
		request := catalog.ForeignRequest{TableOptions:[]catalog.ForeignOption{{Name:"table_name", Value:"source"}}}
		occurrences[i] = exec.ForeignPlanningOccurrence{Source:&exec.ForeignQuerySource{Wrapper:w, Request:request}, Owner:owner, Eligible:true}
	}
	root := &plan.JoinNode{Kind:parser.JoinInner, Left:&plan.SourceNode{Occurrence:0,PresenceSlot:1}, Right:&plan.SourceNode{Occurrence:1,PresenceSlot:2}}
	selected, ok, err := exec.PlanForeignQuery(context.Background(), input, root)
	if err != nil || !ok { t.Fatal(selected,ok,err) }
	if selected.Path.Plan.Private != 1 || selected.Path.TotalCost != 2 || selected.Request.Sources[0].Request.TableOptions[0].Value != "source" || selected.Request.Stage.Slots[0].TypeName != "bool" { t.Fatal("not the first compatible minimum with owned translation",selected) }
	w.paths[1].Plan.Slots[0].TypeName = "changed later"
	if selected.Path.Plan.Slots[0].TypeName != "bool" || w.baseCalls != 0 || w.begins != 0 { t.Fatal("selection aliases provider or entered base/startup",selected) }
	for _, invalid := range []string{"cost","layout"} {
		w.invalid = invalid
		if _,_,err := exec.PlanForeignQuery(context.Background(),input,root); err == nil { t.Fatal("invalid incompatible path escaped validation",invalid) }
	}
	for _, decline := range []string{"decline","incompatible"} {
		w.invalid = decline
		if _,ok,err := exec.PlanForeignQuery(context.Background(),input,root); err != nil || ok { t.Fatal(decline,ok,err) }
	}
	w.invalid = ""
	w.failure = errors.New("discovery failed")
	if _,_,err := exec.PlanForeignQuery(context.Background(),input,root); !errors.Is(err,w.failure) { t.Fatal(err) }
	w.failure = nil
	input.Excluded = true
	if _,ok,err := exec.PlanForeignQuery(context.Background(),input,root); err != nil || ok { t.Fatal("semantic marking barrier lost",ok,err) }
}
