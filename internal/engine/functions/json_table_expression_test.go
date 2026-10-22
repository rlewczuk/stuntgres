package functions

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONTableExpression(t *testing.T) {
	types := temporalTestTypes()
	ctx := &temporalTestContext{Context: context.Background()}
	input := func(id sqltype.ID, text string) sqltype.Value {
		t.Helper()
		v, err := types[id].Input(ctx.Context, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	events := []string(nil)
	child := func(label string, value sqltype.Value) expr.Expr {
		return sqlJSONQueryUnitChild{temporalTestArgument: temporalTestLiteral(value), label: label, events: &events}
	}
	query := func(name string) SQLJSONQuery {
		t.Helper()
		raw := parser.SQLJSONQueryExpr{Kind: "json_value", TableColumn: name, Error: parser.SQLJSONBehavior{Kind: "error"}}
		args := []expr.Expr{child("synthetic document", sqltype.Null(types[sqltype.JSONB])), child(name+" path", input(sqltype.JSONPath, `$`))}
		node, err := NewSQLJSONQuery(expr.Info{Usage: types[sqltype.Int4].Usage()}, raw, args, types[sqltype.JSONB].Usage())
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	members := make([]sqltype.CompositeMember, 5)
	for i, name := range []string{"r", "ao", "av", "bo", "bv"} {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: types[sqltype.Int4].Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	args := []expr.Expr{child("document", input(sqltype.JSONB, `[{"a":[10,20],"b":[30]},{"a":[],"b":[]}]`)), child("root path", input(sqltype.JSONPath, `$[*]`)), child("ordinal placeholder", input(sqltype.Int4, "1")), child("a path", input(sqltype.JSONPath, `$.a[*]`)), query("av"), child("b path", input(sqltype.JSONPath, `$.b[*]`)), query("bv")}
	level := JSONTableLevel{Path: 1, Start: 0, End: 5, Columns: []JSONTableField{{Slot: 0, Child: 2, Ordinality: true}}, Nested: []JSONTableLevel{
		{Path: 3, Start: 1, End: 3, Columns: []JSONTableField{{Slot: 1, Child: 2, Ordinality: true}, {Slot: 2, Child: 4}}},
		{Path: 5, Start: 3, End: 5, Columns: []JSONTableField{{Slot: 3, Child: 2, Ordinality: true}, {Slot: 4, Child: 6}}},
	}}
	node, err := NewJSONTable(expr.Info{Usage: shape.Usage()}, &parser.JSONTable{}, args, level)
	if err != nil {
		t.Fatal(err)
	}
	source, err := node.OpenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]any
	for {
		value, present, err := source.Next(ctx.Context)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		row := make([]any, 5)
		for i := range row {
			v := value.CompositeField(i)
			if !v.IsNull() {
				row[i], err = v.Type().Output(ctx.Context, v)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		rows = append(rows, row)
	}
	want := [][]any{{"1", "1", "10", nil, nil}, {"1", "2", "20", nil, nil}, {"1", nil, nil, "1", "30"}, {"2", nil, nil, nil, nil}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("sibling union/outer extension: %#v", rows)
	}
	wantEvents := []string{"document", "root path", "a path", "b path", "av path", "av path", "bv path", "a path", "b path"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("sibling reset/column order: %v, want %v", events, wantEvents)
	}
	for _, event := range events {
		if event == "synthetic document" || event == "ordinal placeholder" {
			t.Fatalf("inactive/synthetic expression evaluated: %v", events)
		}
	}
	t.Run("sibling reset error precedes columns", func(t *testing.T) {
		bad := append([]expr.Expr(nil), args...)
		bad[5] = child("b path", input(sqltype.JSONPath, `strict $.missing`))
		raw := &parser.JSONTable{Error: parser.SQLJSONBehavior{Kind: "error"}}
		broken, err := NewJSONTable(expr.Info{Usage: shape.Usage()}, raw, bad, level)
		if err != nil {
			t.Fatal(err)
		}
		events = nil
		if _, err := broken.OpenSource(ctx); err == nil {
			t.Fatal("hard sibling path error was suppressed")
		}
		want := []string{"document", "root path", "a path", "b path"}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("effects before sibling error: %v, want %v", events, want)
		}
	})
	rebuilt, err := node.Rebuild(expr.Info{Usage: shape.Usage()}, args)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.ResultUsage().Type == shape {
		t.Fatal("record shape was not reminted")
	}
	// Opening a second invocation must not consume or overwrite the first one's
	// owned values; it also cannot retain any previous invocation's cursor state.
	first, err := node.OpenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	retained, present, err := first.Next(ctx.Context)
	if err != nil || !present {
		t.Fatal(err)
	}
	second, err := node.OpenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	first.Close()
	if text, err := retained.CompositeField(2).Type().Output(ctx.Context, retained.CompositeField(2)); err != nil || text != "10" {
		t.Fatal(text, err)
	}
	bad := append([]expr.Expr(nil), args...)
	bad[0] = child("document", input(sqltype.JSONB, `[{"a":[1,"bad"]}]`))
	broken, err := node.Rebuild(expr.Info{Usage: shape.Usage()}, bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broken.(JSONTable).OpenSource(ctx); err == nil {
		t.Fatal("late conversion escaped eager opening")
	}
}
