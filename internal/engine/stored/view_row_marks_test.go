package stored

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"testing"
)

func TestQuerySetStoredInheritedMarks(t *testing.T) {
	left, right := &plan.SourceNode{Occurrence: 0}, &plan.SourceNode{Occurrence: 1}
	set := &plan.SetNode{Left: &plan.ProjectNode{Child: left}, Right: &plan.ProjectNode{Child: right}}
	wrapped := &plan.ProjectNode{Child: &plan.LimitNode{Child: &plan.SortNode{Child: &plan.EvaluationNode{Child: set}}}}
	local := plan.RowMark{Mode: lock.ForKeyShare, Wait: parser.RowLockNowait, DiagnosticMode: lock.ForShare, Order: 2}
	view := NewView(ViewParts{Query: &plan.Query{Root: wrapped}, Sources: []ViewLeaf{{}, {Mark: local}}})
	outer := plan.RowMark{Mode: lock.ForUpdate, Wait: parser.RowLockSkip, DiagnosticMode: lock.ForNoKeyUpdate, Order: 1}
	for i, want := range []plan.RowMark{{}, local} {
		if got := ViewLeafRowMark(view, i, outer); got != want {
			t.Fatalf("set source %d inherited mark: got %+v want %+v", i, got, want)
		}
		if !exec.QueryHasPhysicalInput([]plan.Node{left, right}[i]) {
			t.Fatal("fixture source lost")
		}
	}
	// General input/dependency traversal must retain both children; only the
	// mark-specific traversal has a boundary. Admission still sees all sources.
	l, r := plan.QueryInputs(set)
	if l != set.Left || r != set.Right || !exec.QueryHasPhysicalInput(wrapped) {
		t.Fatal("set barrier pruned general source traversal")
	}
	ordinary := view.With(func(p *ViewParts) {
		p.Query = &plan.Query{Root: &plan.ProjectNode{Child: &plan.JoinNode{Left: left, Right: right}}}
	})
	for i, want := range []plan.RowMark{outer, exec.MergeQueryRowMark(outer, local)} {
		if got := ViewLeafRowMark(ordinary, i, outer); got != want {
			t.Fatalf("ordinary source %d inherited mark: got %+v want %+v", i, got, want)
		}
	}
	// Encountered sets also stop inherited marks inside an ordinary mixed tree,
	// without clearing marks on the independent physical contributor.
	mixed := view.With(func(p *ViewParts) {
		p.Sources = []ViewLeaf{{}, {}, {}}
		p.Query = &plan.Query{Root: &plan.ProjectNode{Child: &plan.JoinNode{Left: set, Right: &plan.SourceNode{Occurrence: 2}}}}
	})
	for i, want := range []plan.RowMark{{}, {}, outer} {
		if got := ViewLeafRowMark(mixed, i, outer); got != want {
			t.Fatalf("mixed source %d got %+v want %+v", i, got, want)
		}
	}
}
