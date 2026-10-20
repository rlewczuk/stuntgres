package engine

import (
	"context"
	"errors"
	"testing"

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
			schema := querySchema{}
			rows := newFinalRows(schema, 3)
			if width == 2 {
				schema = querySchema{{Name: "id", Usage: integer.Usage()}, {Name: "v", Usage: integer.Usage()}}
				rows.schema = schema
				rows.values = []sqltype.Value{value(1), value(10), value(2), sqltype.Null(integer), value(2), sqltype.Null(integer)}
			}
			definition := &cteDefinition{Owner: 1, Mutation: &binding{returning: &boundReturning{schema: schema}}, Schema: schema, Hint: hint, References: 2}
			if definition.Folded() {
				t.Fatal("modifier folded")
			}
			owner := &cteInvocation{frame: 1, state: queryState{source: &querySource{materialized: true}}}
			var first *sqltype.Value
			if width != 0 {
				first = &rows.values[0]
			}
			if err := owner.completeMutation(definition, Result{Count: 3, Rows: rows}); err != nil {
				t.Fatal(err)
			}
			producer := owner.producers[definition]
			if !producer.done || !producer.closed || producer.opened || producer.affected != 3 || !rows.closed || rows.values != nil || producer.source.flatCount != 3 || producer.source.flatWidth != width {
				t.Fatal("modifier output was not transferred as completed typed rows")
			}
			if width != 0 && &producer.source.flat[0] != first {
				t.Fatal("typed output buffer copied")
			}
			scan := &derivedNode{QuerySchema: schema, CTE: definition}
			state := &queryState{source: owner.state.source, ctes: owner}
			left, err := openCTEScan(context.Background(), state, scan)
			if err != nil {
				t.Fatal(err)
			}
			right, err := openCTEScan(context.Background(), state, scan)
			if err != nil {
				t.Fatal(err)
			}
			if _, present, err := left.nextOccurrence(context.Background()); err != nil || !present {
				t.Fatal(present, err)
			}
			if err := right.drain(context.Background()); err != nil || right.count() != 3 {
				t.Fatal(right.count(), err)
			}
			if err := left.drain(context.Background()); err != nil || left.count() != 3 {
				t.Fatal(left.count(), err)
			}
			if left.source != right.source || left.source != producer.source {
				t.Fatal("readers did not independently replay one typed spool")
			}
			if width != 0 {
				for i, want := range []int64{1, 2, 2} {
					got, err := producer.source.flat[i*width].Integer()
					if err != nil || got != want || (i > 0 && !producer.source.flat[i*width+1].IsNull()) {
						t.Fatal(i, got, err)
					}
				}
			}
			left.Close()
			right.Close()
			if err := owner.close(); err != nil {
				t.Fatal(err)
			}
			fresh := &cteInvocation{frame: 1, state: owner.state}
			if _, err := openCTEScan(context.Background(), &queryState{source: fresh.state.source, ctes: fresh}, scan); err == nil {
				t.Fatal("fresh invocation inherited completed producer")
			}
			if err := owner.completeMutation(definition, Result{}); err == nil {
				t.Fatal("modifier completed twice")
			}
		}
	}
	noReturning := &cteDefinition{Owner: 1, Mutation: &binding{}}
	completion := &cteInvocation{frame: 1}
	if err := completion.completeMutation(noReturning, Result{Count: 2}); err != nil {
		t.Fatal(err)
	}
	modifier := completion.producers[noReturning]
	if !modifier.done || modifier.affected != 2 || modifier.source != nil {
		t.Fatal("no-RETURNING modifier manufactured a relation or lost completion/count")
	}
	if err := completion.close(); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("producer close failure")
	closes := 0
	producer := &cteProducer{opened: true, inner: indexedStage{finish: func() error { closes++; return failure }}}
	owner := &cteInvocation{order: []*cteProducer{producer}}
	if owner.close() != failure || owner.close() != failure || closes != 1 || !producer.closed {
		t.Fatal("close failure was not sticky/once-only", closes)
	}
}
