package exec

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestFTSStatCursorOwnership(t *testing.T) {
	ctx := context.Background()
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "word", Usage: types[sqltype.Text].Usage()}, {Attribute: 2, Name: "ndoc", Usage: types[sqltype.Int4].Usage()}, {Attribute: 3, Name: "nentry", Usage: types[sqltype.Int4].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	vector, err := types[sqltype.TSVector].Input(ctx, "x:1A,2D", -1)
	if err != nil {
		t.Fatal(err)
	}
	columns := []catalog.Column{{Name: "v", Usage: types[sqltype.TSVector].Usage()}}
	root := &plan.ProjectNode{QuerySchema: columns, Child: &plan.SourceNode{QuerySchema: columns}, Frame: 1, Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: columns[0].Usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}}}
	data := make([][]sqltype.Value, 201)
	for i := range data {
		data[i] = []sqltype.Value{vector}
	}
	rows, err := OpenNestedQueryRows(ctx, root, &Source{Anonymous: data}, nil)
	if err != nil {
		t.Fatal(err)
	}
	values, err := ConsumeFTSStatistics(ctx, rows, "", shape)
	if err != nil || len(values) != 1 {
		t.Fatal("complete producer", values, err)
	}
	if value, err := values[0].CompositeField(1).Integer(); err != nil || value != 201 {
		t.Fatal("documents", value, err)
	}
	if value, err := values[0].CompositeField(2).Integer(); err != nil || value != 402 {
		t.Fatal("entries", value, err)
	}
	if data[0][0] != vector || data[200][0] != vector {
		t.Fatal("borrowed source changed")
	}
	var scratch [1]sqltype.Value
	if _, err := rows.Next(ctx, scratch[:]); err == nil {
		t.Fatal("consumer retained live cursor")
	}
	rows, err = OpenNestedQueryRows(ctx, root, &Source{Anonymous: data}, nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	values, err = ConsumeFTSStatistics(canceled, rows, "a", shape)
	if values != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("partial cancellation publication", values, err)
	}
	if _, err := rows.Next(ctx, scratch[:]); err == nil {
		t.Fatal("canceled consumer retained cursor")
	}
}
