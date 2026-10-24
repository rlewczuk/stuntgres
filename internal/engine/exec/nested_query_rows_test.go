package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// TestPLpgSQLQueryLoopLifetime covers the producer and CTE row owner that
// PL/pgSQL query loops borrow; the adapter's loop lifetime units stay in the root.
func TestPLpgSQLQueryLoopLifetime(t *testing.T) {
	t.Run("retained_cte", queryLoopCTELifetime)
	t.Run("close_failure", queryLoopCloseFailure)
}

func queryLoopInteger(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			return d
		}
	}
	t.Fatal("int4 descriptor missing")
	return nil
}

func queryLoopCTELifetime(t *testing.T) {
	integer := queryLoopInteger(t)
	one, _ := sqltype.Integer(integer, 1)
	two, _ := sqltype.Integer(integer, 2)
	schema := []catalog.Column{{Name: "i", Usage: integer.Usage()}}
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
	inner := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Expressions: []expr.Expr{column}, Frame: 1}
	definition := &plan.CTEDefinition{Owner: 1, Query: &plan.Query{Root: inner}, References: 2}
	source := &Source{Anonymous: [][]sqltype.Value{{one}, {two}}}
	parent := &CTEInvocation{Frame: 9}
	owner := &CTEInvocation{Frame: 1, Parent: parent, State: State{Source: source, SourceMode: SourceReusable}}
	owner.State.Ctes = owner
	scan := &plan.DerivedNode{QuerySchema: schema, Query: definition.Query, CTE: definition, ScanFrame: 1}
	outer := &plan.ProjectNode{QuerySchema: schema, Child: scan, Expressions: []expr.Expr{column}, Frame: 1}
	rows := &nestedQueryRows{schema: schema, ctx: context.Background(), state: State{Source: source, SourceMode: SourceReusable, Ctes: owner}}
	stage, err := OpenQueryProjectStage(context.Background(), &rows.state, outer)
	if err != nil {
		t.Fatal(err)
	}
	rows.stage = stage
	retained := &CTEOwnedRows{RowResult: rows, Owner: owner, Previous: parent, Source: source}
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

func queryLoopCloseFailure(t *testing.T) {
	integer := queryLoopInteger(t)
	schema := []catalog.Column{{Name: "i", Usage: integer.Usage()}}
	value, _ := sqltype.Integer(integer, 1)
	root := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Frame: 1, Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 1, Slot: 0}}}
	source := &Source{Anonymous: [][]sqltype.Value{{value}}}
	opened, err := OpenNestedQueryRows(context.Background(), root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := opened.(*nestedQueryRows)
	failure := errors.New("source cleanup veto")
	rows.stage.Finish = func() error { return failure }
	owner := &CTEInvocation{Frame: 1}
	owned := &CTEOwnedRows{RowResult: rows, Owner: owner, Source: source}
	if err := owned.Close(); !errors.Is(err, failure) || !owner.Closed || !rows.closed {
		t.Fatal("cleanup failure lost ownership", err)
	}
	if err := owned.Close(); err != nil {
		t.Fatal("duplicate cleanup", err)
	}
}
