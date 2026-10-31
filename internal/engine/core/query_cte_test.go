package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func cteUnitType(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	typ, ok := engine.New().CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4 descriptor not found")
	}
	return typ
}

func cteUnitValue(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func cteUnitScan(expressions []expr.Expr, schema plan.QuerySchema, rows int) (*exec.CTEInvocation, *exec.State, *plan.DerivedNode) {
	project := &plan.ProjectNode{QuerySchema: schema, Child: &plan.ValuesNode{Rows: make([][]expr.Expr, rows)}, Expressions: expressions, Frame: 2}
	definition := &plan.CTEDefinition{Query: &plan.Query{Root: project}, Owner: 1, References: 2, Hint: parser.CTEMaterialized}
	state := &exec.State{Source: &exec.Source{Materialized: true}}
	owner := &exec.CTEInvocation{Frame: 1, State: *state}
	state.Ctes = owner
	return owner, state, &plan.DerivedNode{QuerySchema: schema, Query: definition.Query, CTE: definition}
}

func cteUnitNext(t *testing.T, stage *exec.Stage) []sqltype.Value {
	t.Helper()
	index, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present {
		t.Fatal(index, present, err)
	}
	return stage.Source.Row(stage.Index(index))
}
