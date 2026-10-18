package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterAddColumnProjection(t *testing.T) {
	e := New()
	u := testUsage(e, sqltype.Int4)
	old := &relationDescriptor{Columns: []ColumnDescriptor{{Name: "a", Usage: u}, {Name: "b", Usage: u}}, Attributes: []int32{1, 2}, AttributeHighWater: 2}
	next := &relationDescriptor{Columns: []ColumnDescriptor{{Name: "a", Usage: u}, {Name: "c", Usage: u}}, Attributes: []int32{1, 3}, AttributeHighWater: 3}
	one, err := sqltype.Integer(u.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	seven, err := sqltype.Integer(u.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	rows := []rowVersion{{values: []sqltype.Value{one, seven}}}
	before := append([]sqltype.Value(nil), rows[0].values...)
	for _, fill := range []sqltype.Value{sqltype.Null(u.Type), seven} {
		projected, err := projectVersions(context.Background(), rows, old, next, map[int32]sqltype.Value{3: fill})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(projected[0].values, []sqltype.Value{one, fill}) || !reflect.DeepEqual(rows[0].values, before) {
			t.Fatal("projection changed source or copied wrong slot", projected, rows)
		}
		original, result := rows[0], projected[0]
		original.values, result.values = nil, nil
		if !reflect.DeepEqual(original, result) {
			t.Fatal("row identity changed", original, result)
		}
	}
	if _, err := projectVersions(context.Background(), rows, old, next, nil); err == nil {
		t.Fatal("missing captured fill accepted")
	}
	bad := []rowVersion{{values: []sqltype.Value{one}}}
	if _, err := projectVersions(context.Background(), bad, old, next, map[int32]sqltype.Value{3: seven}); err == nil {
		t.Fatal("invalid original width accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := projectVersions(ctx, rows, old, next, map[int32]sqltype.Value{3: seven}); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestAlterAddColumnZeroWidthProjection(t *testing.T) {
	e := New()
	u := testUsage(e, sqltype.Text)
	old := &relationDescriptor{AttributeHighWater: 3}
	next := &relationDescriptor{Columns: []ColumnDescriptor{{Name: "new", Usage: u}}, Attributes: []int32{4}, AttributeHighWater: 4}
	rows := []rowVersion{{}, {}}
	projected, err := projectVersions(context.Background(), rows, old, next, map[int32]sqltype.Value{4: sqltype.Null(u.Type)})
	if err != nil || len(projected) != 2 {
		t.Fatal(projected, err)
	}
	for _, row := range projected {
		if len(row.values) != 1 || !row.values[0].IsNull() || row.values[0].Type() != u.Type {
			t.Fatal(row)
		}
	}
	for _, row := range rows {
		if len(row.values) != 0 {
			t.Fatal("source widened", row)
		}
	}
}

func TestAlterAddColumnCancellationDuringShapeTraversal(t *testing.T) {
	e := New()
	u := testUsage(e, sqltype.Int4)
	one, err := sqltype.Integer(u.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	old := &relationDescriptor{Columns: []ColumnDescriptor{{Name: "a", Usage: u}}, Attributes: []int32{1}, AttributeHighWater: 1}
	next := &relationDescriptor{Columns: []ColumnDescriptor{{Name: "a", Usage: u}, {Name: "b", Usage: u}}, Attributes: []int32{1, 2}, AttributeHighWater: 2}
	rows := []rowVersion{{row: 1, version: 1, values: []sqltype.Value{one}}, {row: 2, version: 2, values: []sqltype.Value{one}}, {row: 3, version: 3, values: []sqltype.Value{one}}}
	projection, err := newColumnProjection(old, next, map[int32]sqltype.Value{2: sqltype.Null(u.Type)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	traversal := &cancelAfterPreflightContext{Context: ctx, cancel: cancel}
	projected, err := projection.versions(traversal, rows)
	if err != context.Canceled || projected != nil || traversal.checks != 3 {
		t.Fatal(projected, err, traversal.checks)
	}
	for i, row := range rows {
		if row.row != ids.RowID(i+1) || len(row.values) != 1 || row.values[0] != one {
			t.Fatal("canceled traversal changed source", rows)
		}
	}
	healthy, err := projection.versions(context.Background(), rows)
	if err != nil || len(healthy) != 3 {
		t.Fatal(healthy, err)
	}
}
