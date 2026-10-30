package core_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
)

type fdwExplainOwnedPlanWrapper struct {
	fdwUnitWrapper
	original []int32
}

func (w *fdwExplainOwnedPlanWrapper) GetForeignPlan(_ context.Context, request catalog.ForeignRequest, path catalog.ForeignPath) (catalog.ForeignPlan, error) {
	w.chosen = path
	// A provider may reuse its returned path vector or the input argument after
	// this callback. Neither is the retained selected-path/plan layout owner.
	w.original[0] = 1
	path.Attributes[0] = 1
	return catalog.ForeignPlan{Attributes: []int32{3}, Private: path.Private}, nil
}

func TestFDWExplainSelectedPlanOwnership(t *testing.T) {
	layout := []int32{3}
	w := &fdwExplainOwnedPlanWrapper{original: layout, fdwUnitWrapper: fdwUnitWrapper{paths: []catalog.ForeignPath{
		{TotalCost: 50, Attributes: []int32{1, 3}, Private: "first-expensive"},
		{TotalCost: 2, Attributes: layout, Private: "second-selected"},
		{TotalCost: 2, Attributes: []int32{3}, Private: "third-tied"},
	}}}
	request := catalog.ForeignRequest{Columns: []catalog.ForeignColumn{{Attribute: 1}, {Attribute: 3}}, RequiredAttributes: []int32{3}}
	source := &exec.ForeignQuerySource{Wrapper: w}
	execution := &exec.ForeignExecution{}
	if err := source.Plan(context.Background(), request, execution); err != nil {
		t.Fatal(err)
	}
	if execution.Size.Rows != 3 || execution.Path.Private != "second-selected" || execution.Plan.Private != execution.Path.Private || !slices.Equal(execution.Path.Attributes, []int32{3}) || !slices.Equal(execution.Plan.Attributes, []int32{3}) || !slices.Equal(execution.Slots, []int{1}) {
		t.Fatal("selected facts were lost or borrowed", execution)
	}
	if &execution.Path.Attributes[0] == &layout[0] || &execution.Path.Attributes[0] == &w.chosen.Attributes[0] || &execution.Path.Attributes[0] == &execution.Plan.Attributes[0] {
		t.Fatal("path/argument/plan layouts share mutable backing")
	}
	if !slices.Equal(request.RequiredAttributes, []int32{3}) {
		t.Fatal("planning mutated caller attributes")
	}
}
