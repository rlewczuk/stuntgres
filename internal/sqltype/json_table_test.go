package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONTablePlan(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	p, err := BindJSONTableRows(types[JSONB].Usage(), types[JSONPath].Usage(), types[JSONB].Usage(), []Usage{types[Int4].Usage(), types[Int4].Usage()}, []string{"x", "x"})
	if err != nil {
		t.Fatal(err)
	}
	input := func(id ID, text string) Value { return jsonbSetUnitInput(t, types[id], text) }
	invocation, err := p.Start(ctx, []Value{input(Int4, "1"), input(Int4, "2")}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	document := input(JSONB, `[{"x":1},{}]`)
	rows, err := invocation.Select(ctx, document, input(JSONPath, `strict $[*].x`), false, JSONPathEnvironment{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("suppressed prefix: %v %v", rows, err)
	}
	_, err = invocation.Select(ctx, document, input(JSONPath, `strict $[*].x`), true, JSONPathEnvironment{})
	if d, ok := err.(*sqlstate.Error); !ok || d.Code != "2203A" {
		t.Fatalf("strict failure: %v", err)
	}
	rows, err = invocation.Select(ctx, document, input(JSONPath, `$x`), false, JSONPathEnvironment{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("typed binding: %v %v", rows, err)
	}
	text, err := rows[0].Type().Output(ctx, rows[0])
	if err != nil || text != "1" {
		t.Fatal(text, err)
	}
	// Selected row values obey the ordinary JSONB codec contract, not only
	// the downstream column evaluator's tree-payload contract.
	rows, err = invocation.Select(ctx, document, input(JSONPath, `$[*]`), false, JSONPathEnvironment{})
	if err != nil || len(rows) != 2 {
		t.Fatalf("row payloads: %v %v", rows, err)
	}
	for i, want := range []string{`{"x": 1}`, `{}`} {
		text, err := rows[i].Type().Output(ctx, rows[i])
		if err != nil || text != want {
			t.Fatalf("row %d output: %q %v; want %q", i, text, err, want)
		}
	}
	_, err = invocation.Select(ctx, document, input(JSONPath, `$missing`), false, JSONPathEnvironment{})
	if d, ok := err.(*sqlstate.Error); !ok || d.Code != "42704" {
		t.Fatalf("hard missing variable: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = invocation.Select(canceled, Null(types[JSONB]), input(JSONPath, `$`), false, JSONPathEnvironment{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	foreign := testTypes()
	if _, err = invocation.Select(ctx, Null(foreign[JSONB]), input(JSONPath, `$`), false, JSONPathEnvironment{}); err == nil {
		t.Fatal("foreign owner accepted")
	}
	for _, id := range []ID{Int4, Text, Int2, Int8} {
		column, err := BindSQLJSONTableColumn("json_exists", "", false, types[id].Usage(), types[JSONB].Usage(), types[JSONPath].Usage(), types[JSONB].Usage(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		column = column.TableColumn("exists_column", types[id].Usage())
		outcome, err := column.Evaluate(ctx, document, input(JSONPath, `$`), nil, JSONPathEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if id == Int2 || id == Int8 {
			if outcome.Error == nil {
				t.Fatalf("unexpected boolean numeric conversion: %v", id)
			}
			continue
		}
		text, err := outcome.Value.Type().Output(ctx, outcome.Value)
		want := "true"
		if id == Int4 {
			want = "1"
		}
		if err != nil || text != want {
			t.Fatal(id, text, err)
		}
	}
}
