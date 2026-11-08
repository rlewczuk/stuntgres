package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestGroupDependencyParallelPayloadMerge(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	integer, text, json := types[sqltype.Int4], types[sqltype.Text], types[sqltype.JSON]
	kernel, err := sqltype.BindGrouping(integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqltype.BindGrouping(json.Usage()); err == nil {
		t.Fatal("JSON fixture unexpectedly supplies grouping equality")
	}
	provider := functions.NewStringAggregateProvider(text.Usage())
	node := &plan.AggregateNode{
		QuerySchema: plan.QuerySchema{{Usage: integer.Usage()}, {Usage: text.Usage()}, {Usage: integer.Usage()}, {Usage: json.Usage()}, {Usage: text.Usage()}},
		Keys:        []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 1}},
		Grouping:    []plan.Grouping{{Grouping: kernel}},
		Calls:       []plan.AggregateCall{{Provider: provider}},
		Dependent:   []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: json.Usage()}, Slot: 1, Scope: 1, Frame: 1}, plan.ColumnExpr{Info: expr.Info{Usage: text.Usage()}, Slot: 2, Scope: 1, Frame: 1}},
	}
	ctx := context.Background()
	evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
	partial := func(id int64, input, payload string) QueryGroup {
		key, err := sqltype.Integer(integer, id)
		if err != nil {
			t.Fatal(err)
		}
		jsonValue, err := json.Input(ctx, payload, -1)
		if err != nil {
			t.Fatal(err)
		}
		state, err := provider.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.Advance(ctx, state, []sqltype.Value{sqltype.String(text, input), sqltype.String(text, ",")}, evaluation); err != nil {
			t.Fatal(err)
		}
		// Exercise the same real opaque transport consumed by worker merge.
		data, present, err := provider.Serialize(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		occurrence, err := sqltype.Integer(integer, 0)
		if err != nil {
			t.Fatal(err)
		}
		return QueryGroup{Keys: []sqltype.Value{key, sqltype.Null(text), occurrence, jsonValue, sqltype.Null(text)}, states: []any{aggregateTransport{data: data, present: present}}}
	}
	first := partial(1, "a", `{"representative":"first"}`)
	second := partial(1, "b", `{"representative":"different"}`)
	other := partial(2, "c", `{"representative":"other"}`)
	runs := [][]QueryGroup{{first}, {second, other}}
	rows, err := finalizeAggregatePartitions(ctx, node, runs, evaluation)
	if err != nil || len(rows) != 2 {
		t.Fatal("different non-key payload split the merged group", len(rows), err)
	}
	combined, err := rows[0][1].Text()
	if err != nil || combined != "a,b" || !sqltype.SameDatum(rows[0][3], first.Keys[3]) || !rows[0][4].IsNull() {
		t.Fatal("merge lost first JSON/NULL payload or unchanged call slot", rows[0], err)
	}
	if !first.Keys[1].IsNull() || !second.Keys[1].IsNull() || &rows[0][0] == &first.Keys[0] {
		t.Fatal("leader finalization mutated or borrowed partial row backing")
	}
	value, err := rows[1][1].Text()
	if err != nil || value != "c" || !sqltype.SameDatum(rows[1][3], other.Keys[3]) {
		t.Fatal("independent group payload changed", rows[1], err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if rows, err := finalizeAggregatePartitions(canceled, node, runs, evaluation); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("canceled merge published partial rows", rows, err)
	}
}
