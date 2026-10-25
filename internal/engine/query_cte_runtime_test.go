package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func TestQueryCTESuccessfulCloseFailureVeto(t *testing.T) {
	owner, state, node := cteUnitScan(nil, nil, 1)
	stage, err := exec.OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &stage)
	producer := owner.Producers[node.CTE]
	failure := errors.New("required close failed")
	closes := 0
	producer.Inner.Finish = func() error { closes++; return failure }
	if _, err := producer.Ensure(context.Background(), 1); err != failure {
		t.Fatal(err)
	}
	if _, err := producer.Ensure(context.Background(), 0); err != failure {
		t.Fatal(err)
	}
	if err := owner.Close(); err != failure || closes != 1 {
		t.Fatal(err, closes)
	}
}

func TestQueryCTEMandatoryPhysicalFrontier(t *testing.T) {
	marked := &exec.Source{Occurrence: 0}
	unmarked := &exec.Source{Occurrence: 1}
	locks := &exec.LockContext{Leaves: []exec.QueryLockLeaf{
		{Source: marked, Mark: plan.RowMark{Mode: lock.ForUpdate}},
		{Source: unmarked},
	}}
	marked.Locks, unmarked.Locks = locks, locks
	inputs := &exec.Source{Sources: []*exec.Source{marked, unmarked}}
	physical := &plan.SourceNode{Occurrence: 0}
	unmarkedPhysical := &plan.SourceNode{Occurrence: 1}
	aggregate := &plan.AggregateNode{Child: physical}
	if len(exec.PendingQueryMarks(aggregate, inputs, nil)) != 0 || !exec.QueryHasMandatoryPhysicalFrontier(aggregate, inputs) {
		t.Fatal("consumed physical marks mistaken for absent demand")
	}
	storedInputs := &exec.Source{Sources: []*exec.Source{unmarked, marked}}
	stored := &exec.Source{NestedRoot: &plan.ProjectNode{Child: &plan.AggregateNode{Child: &plan.SourceNode{Occurrence: 1}}}, NestedInputs: storedInputs}
	adapter := &exec.Source{Input: stored}
	if !exec.QueryHasMandatoryPhysicalFrontier(&plan.SourceNode{}, adapter) {
		t.Fatal("stored adapter lost nestedInputs occurrence mapping")
	}
	ordinary := &plan.DerivedNode{Query: &plan.Query{Root: &plan.ProjectNode{Child: aggregate}}}
	if !exec.QueryHasMandatoryPhysicalFrontier(ordinary, inputs) {
		t.Fatal("ordinary derived boundary hid mandatory input")
	}
	owners := []plan.Node{
		&plan.SortNode{Child: physical}, aggregate, &plan.DistinctNode{Child: physical},
		&plan.DistinctOnNode{Child: physical}, &plan.SetNode{Left: &plan.ProjectNode{Child: physical}, Right: &plan.ProjectNode{Child: unmarkedPhysical}},
		&plan.LimitNode{Child: physical}, &plan.WindowNode{Child: physical},
	}
	for _, owner := range owners {
		if !exec.QueryHasMandatoryPhysicalFrontier(owner, inputs) {
			t.Fatalf("consuming owner %T lost marked input", owner)
		}
	}
	if exec.QueryHasMandatoryPhysicalFrontier(&plan.AggregateNode{Child: unmarkedPhysical}, inputs) ||
		exec.QueryHasMandatoryPhysicalFrontier(&plan.JoinNode{Left: physical, Right: &plan.AggregateNode{Child: unmarkedPhysical}}, inputs) {
		t.Fatal("unrelated marked branch contaminated unmarked owner")
	}
	locks.IdentityOnly = true
	if exec.QueryHasMandatoryPhysicalFrontier(aggregate, inputs) {
		t.Fatal("identity-only manifest treated as semantic locking")
	}
	locks.IdentityOnly = false
	for _, hint := range []parser.CTEMaterialization{parser.CTEDefault, parser.CTEMaterialized, parser.CTENotMaterialized} {
		definition := &plan.CTEDefinition{Hint: hint, Query: ordinary.Query}
		opaque := &plan.DerivedNode{Query: ordinary.Query, CTE: definition}
		if exec.QueryHasMandatoryPhysicalFrontier(opaque, inputs) ||
			exec.QueryHasMandatoryPhysicalFrontier(&plan.AggregateNode{Child: opaque}, inputs) {
			t.Fatal("consumer inspected opaque CTE body's physical frontier", hint)
		}
		if !exec.QueryHasMandatoryPhysicalFrontier(&plan.JoinNode{Left: opaque, Right: aggregate}, inputs) {
			t.Fatal("opaque CTE hid independently reachable physical owner", hint)
		}
		analysisOnly := &plan.ProjectNode{Child: &plan.SingletonNode{}, AnalysisExpressions: []expr.Expr{plan.SubqueryExpr{CTEEdge: true, QueryPlan: &plan.Query{Root: &plan.ProjectNode{Child: aggregate}}}}}
		if exec.QueryHasMandatoryPhysicalFrontier(analysisOnly, inputs) {
			t.Fatal("unused analysis definition became executable demand")
		}
	}
}
