package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCTEDMLInvocationOwnership(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, width := range []int{0, 2} {
		for _, hint := range []parser.CTEMaterialization{parser.CTEDefault, parser.CTEMaterialized, parser.CTENotMaterialized} {
			schema := plan.QuerySchema{}
			rows := exec.NewFinalRows(schema, 3)
			if width == 2 {
				schema = plan.QuerySchema{{Name: "id", Usage: integer.Usage()}, {Name: "v", Usage: integer.Usage()}}
				rows.Columns = schema
				rows.Values = []sqltype.Value{value(1), value(10), value(2), sqltype.Null(integer), value(2), sqltype.Null(integer)}
			}
			definition := &plan.CTEDefinition{Owner: 1, Mutation: &binding{Returning: &boundReturning{Schema: schema}}, Schema: schema, Hint: hint, References: 2}
			if definition.Folded() {
				t.Fatal("modifier folded")
			}
			owner := &exec.CTEInvocation{Frame: 1, State: exec.State{Source: &exec.Source{Materialized: true}}}
			var first *sqltype.Value
			if width != 0 {
				first = &rows.Values[0]
			}
			if err := completeCTEMutation(owner, definition, Result{Count: 3, Rows: rows}); err != nil {
				t.Fatal(err)
			}
			producer := owner.Producers[definition]
			if !producer.Done || !producer.Closed || producer.Opened || producer.Affected != 3 || !rows.Closed || rows.Values != nil || producer.Source.FlatCount != 3 || producer.Source.FlatWidth != width {
				t.Fatal("modifier output was not transferred as completed typed rows")
			}
			if width != 0 && &producer.Source.Flat[0] != first {
				t.Fatal("typed output buffer copied")
			}
			scan := &plan.DerivedNode{QuerySchema: schema, CTE: definition}
			state := &exec.State{Source: owner.State.Source, Ctes: owner}
			left, err := exec.OpenCTEScan(context.Background(), state, scan)
			if err != nil {
				t.Fatal(err)
			}
			right, err := exec.OpenCTEScan(context.Background(), state, scan)
			if err != nil {
				t.Fatal(err)
			}
			if _, present, err := left.NextOccurrence(context.Background()); err != nil || !present {
				t.Fatal(present, err)
			}
			if err := right.Drain(context.Background()); err != nil || right.Count() != 3 {
				t.Fatal(right.Count(), err)
			}
			if err := left.Drain(context.Background()); err != nil || left.Count() != 3 {
				t.Fatal(left.Count(), err)
			}
			if left.Source != right.Source || left.Source != producer.Source {
				t.Fatal("readers did not independently replay one typed spool")
			}
			if width != 0 {
				for i, want := range []int64{1, 2, 2} {
					got, err := producer.Source.Flat[i*width].Integer()
					if err != nil || got != want || (i > 0 && !producer.Source.Flat[i*width+1].IsNull()) {
						t.Fatal(i, got, err)
					}
				}
			}
			left.Close()
			right.Close()
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			fresh := &exec.CTEInvocation{Frame: 1, State: owner.State}
			if _, err := exec.OpenCTEScan(context.Background(), &exec.State{Source: fresh.State.Source, Ctes: fresh}, scan); err == nil {
				t.Fatal("fresh invocation inherited completed producer")
			}
			if err := completeCTEMutation(owner, definition, Result{}); err == nil {
				t.Fatal("modifier completed twice")
			}
		}
	}
	noReturning := &plan.CTEDefinition{Owner: 1, Mutation: &binding{}}
	completion := &exec.CTEInvocation{Frame: 1}
	if err := completeCTEMutation(completion, noReturning, Result{Count: 2}); err != nil {
		t.Fatal(err)
	}
	modifier := completion.Producers[noReturning]
	if !modifier.Done || modifier.Affected != 2 || modifier.Source != nil {
		t.Fatal("no-RETURNING modifier manufactured a relation or lost completion/count")
	}
	if err := completion.Close(); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("producer close failure")
	closes := 0
	producer := &exec.CTEProducer{Opened: true, Inner: exec.Stage{Finish: func() error { closes++; return failure }}}
	owner := &exec.CTEInvocation{Order: []*exec.CTEProducer{producer}}
	if owner.Close() != failure || owner.Close() != failure || closes != 1 || !producer.Closed {
		t.Fatal("close failure was not sticky/once-only", closes)
	}
}
