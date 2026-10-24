package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func queryLoopCTELifetime(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			integer = d
		}
	}
	one, _ := sqltype.Integer(integer, 1)
	two, _ := sqltype.Integer(integer, 2)
	schema := []catalog.Column{{Name: "i", Usage: integer.Usage()}}
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
	inner := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Expressions: []expr.Expr{column}, Frame: 1}
	definition := &plan.CTEDefinition{Owner: 1, Query: &plan.Query{Root: inner}, References: 2}
	source := &exec.Source{Anonymous: [][]sqltype.Value{{one}, {two}}}
	parent := &exec.CTEInvocation{Frame: 9}
	owner := &exec.CTEInvocation{Frame: 1, Parent: parent, State: exec.State{Source: source, SourceMode: exec.SourceReusable}}
	owner.State.Ctes = owner
	scan := &plan.DerivedNode{QuerySchema: schema, Query: definition.Query, CTE: definition, ScanFrame: 1}
	outer := &plan.ProjectNode{QuerySchema: schema, Child: scan, Expressions: []expr.Expr{column}, Frame: 1}
	rows := &nestedQueryRows{schema: schema, ctx: context.Background(), state: exec.State{Source: source, SourceMode: exec.SourceReusable, Ctes: owner}}
	stage, err := exec.OpenQueryProjectStage(context.Background(), &rows.state, outer)
	if err != nil {
		t.Fatal(err)
	}
	rows.stage = stage
	retained := &cteOwnedRows{RowResult: rows, owner: owner, previous: parent, source: source}
	defer retained.Close()
	var row [1]sqltype.Value
	for _, want := range []sqltype.Value{one, two} {
		present, err := retained.Next(context.Background(), row[:])
		if err != nil || !present || row[0] != want {
			t.Fatal("retained CTE failed demand", present, row, err)
		}
		if owner.Closed || parent.Closed {
			t.Fatal("borrowed scope restored by retiring a live CTE")
		}
	}
	producer := owner.Producers[definition]
	if producer == nil || !producer.Opened || producer.Closed {
		t.Fatal("CTE was not a live demand producer")
	}
	// Stop before EOF: main reader cleanup must retire the retained producer,
	// without closing the enclosing invocation borrowed by this lexical scope.
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	if !rows.closed || !owner.Closed || !producer.Closed || parent.Closed {
		t.Fatal("transfer cleanup closed wrong CTE owner")
	}
	if err := retained.Close(); err != nil {
		t.Fatal("duplicate owner close", err)
	}
}
