package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryCTESuccessfulCloseFailureVeto(t *testing.T) {
	owner, state, node := cteUnitScan(nil, nil, 1)
	stage, err := OpenCTEScan(context.Background(), state, node)
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
	marked := &Source{Occurrence: 0}
	unmarked := &Source{Occurrence: 1}
	locks := &LockContext{Leaves: []QueryLockLeaf{
		{Source: marked, Mark: plan.RowMark{Mode: lock.ForUpdate}},
		{Source: unmarked},
	}}
	marked.Locks, unmarked.Locks = locks, locks
	inputs := &Source{Sources: []*Source{marked, unmarked}}
	physical := &plan.SourceNode{Occurrence: 0}
	unmarkedPhysical := &plan.SourceNode{Occurrence: 1}
	aggregate := &plan.AggregateNode{Child: physical}
	if len(PendingQueryMarks(aggregate, inputs, nil)) != 0 || !QueryHasMandatoryPhysicalFrontier(aggregate, inputs) {
		t.Fatal("consumed physical marks mistaken for absent demand")
	}
	storedInputs := &Source{Sources: []*Source{unmarked, marked}}
	stored := &Source{NestedRoot: &plan.ProjectNode{Child: &plan.AggregateNode{Child: &plan.SourceNode{Occurrence: 1}}}, NestedInputs: storedInputs}
	adapter := &Source{Input: stored}
	if !QueryHasMandatoryPhysicalFrontier(&plan.SourceNode{}, adapter) {
		t.Fatal("stored adapter lost nestedInputs occurrence mapping")
	}
	ordinary := &plan.DerivedNode{Query: &plan.Query{Root: &plan.ProjectNode{Child: aggregate}}}
	if !QueryHasMandatoryPhysicalFrontier(ordinary, inputs) {
		t.Fatal("ordinary derived boundary hid mandatory input")
	}
	owners := []plan.Node{
		&plan.SortNode{Child: physical}, aggregate, &plan.DistinctNode{Child: physical},
		&plan.DistinctOnNode{Child: physical}, &plan.SetNode{Left: &plan.ProjectNode{Child: physical}, Right: &plan.ProjectNode{Child: unmarkedPhysical}},
		&plan.LimitNode{Child: physical}, &plan.WindowNode{Child: physical},
	}
	for _, owner := range owners {
		if !QueryHasMandatoryPhysicalFrontier(owner, inputs) {
			t.Fatalf("consuming owner %T lost marked input", owner)
		}
	}
	if QueryHasMandatoryPhysicalFrontier(&plan.AggregateNode{Child: unmarkedPhysical}, inputs) ||
		QueryHasMandatoryPhysicalFrontier(&plan.JoinNode{Left: physical, Right: &plan.AggregateNode{Child: unmarkedPhysical}}, inputs) {
		t.Fatal("unrelated marked branch contaminated unmarked owner")
	}
	locks.IdentityOnly = true
	if QueryHasMandatoryPhysicalFrontier(aggregate, inputs) {
		t.Fatal("identity-only manifest treated as semantic locking")
	}
	locks.IdentityOnly = false
	for _, hint := range []parser.CTEMaterialization{parser.CTEDefault, parser.CTEMaterialized, parser.CTENotMaterialized} {
		definition := &plan.CTEDefinition{Hint: hint, Query: ordinary.Query}
		opaque := &plan.DerivedNode{Query: ordinary.Query, CTE: definition}
		if QueryHasMandatoryPhysicalFrontier(opaque, inputs) ||
			QueryHasMandatoryPhysicalFrontier(&plan.AggregateNode{Child: opaque}, inputs) {
			t.Fatal("consumer inspected opaque CTE body's physical frontier", hint)
		}
		if !QueryHasMandatoryPhysicalFrontier(&plan.JoinNode{Left: opaque, Right: aggregate}, inputs) {
			t.Fatal("opaque CTE hid independently reachable physical owner", hint)
		}
		analysisOnly := &plan.ProjectNode{Child: &plan.SingletonNode{}, AnalysisExpressions: []expr.Expr{plan.SubqueryExpr{CTEEdge: true, QueryPlan: &plan.Query{Root: &plan.ProjectNode{Child: aggregate}}}}}
		if QueryHasMandatoryPhysicalFrontier(analysisOnly, inputs) {
			t.Fatal("unused analysis definition became executable demand")
		}
	}
}

func cteUnitScan(expressions []expr.Expr, schema plan.QuerySchema, rows int) (*CTEInvocation, *State, *plan.DerivedNode) {
	project := &plan.ProjectNode{QuerySchema: schema, Child: &plan.ValuesNode{Rows: make([][]expr.Expr, rows)}, Expressions: expressions, Frame: 2}
	definition := &plan.CTEDefinition{Query: &plan.Query{Root: project}, Owner: 1, References: 2, Hint: parser.CTEMaterialized}
	state := &State{Source: &Source{Materialized: true}}
	owner := &CTEInvocation{Frame: 1, State: *state}
	state.Ctes = owner
	return owner, state, &plan.DerivedNode{QuerySchema: schema, Query: definition.Query, CTE: definition}
}

func cteUnitNext(t *testing.T, stage *Stage) []sqltype.Value {
	t.Helper()
	index, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present {
		t.Fatal(index, present, err)
	}
	return stage.Source.Row(stage.Index(index))
}
