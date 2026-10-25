package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestAlterAddColumnProjection(t *testing.T) {
	e := engine.New()
	u := enginetest.Usage(e, sqltype.Int4)
	old := &catalog.Relation{Columns: []catalog.Column{{Name: "a", Usage: u}, {Name: "b", Usage: u}}, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}}
	next := &catalog.Relation{Columns: []catalog.Column{{Name: "a", Usage: u}, {Name: "c", Usage: u}}, Attributes: []int32{1, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}}
	one, err := sqltype.Integer(u.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	seven, err := sqltype.Integer(u.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	rows := []storage.RowVersion{{Values: []sqltype.Value{one, seven}}}
	before := append([]sqltype.Value(nil), rows[0].Values...)
	for _, fill := range []sqltype.Value{sqltype.Null(u.Type), seven} {
		projected, err := session.ProjectVersions(context.Background(), rows, old, next, map[int32]sqltype.Value{3: fill})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(projected[0].Values, []sqltype.Value{one, fill}) || !reflect.DeepEqual(rows[0].Values, before) {
			t.Fatal("projection changed source or copied wrong slot", projected, rows)
		}
		original, result := rows[0], projected[0]
		original.Values, result.Values = nil, nil
		if !reflect.DeepEqual(original, result) {
			t.Fatal("row identity changed", original, result)
		}
	}
	if _, err := session.ProjectVersions(context.Background(), rows, old, next, nil); err == nil {
		t.Fatal("missing captured fill accepted")
	}
	bad := []storage.RowVersion{{Values: []sqltype.Value{one}}}
	if _, err := session.ProjectVersions(context.Background(), bad, old, next, map[int32]sqltype.Value{3: seven}); err == nil {
		t.Fatal("invalid original width accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.ProjectVersions(ctx, rows, old, next, map[int32]sqltype.Value{3: seven}); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestAlterAddColumnZeroWidthProjection(t *testing.T) {
	e := engine.New()
	u := enginetest.Usage(e, sqltype.Text)
	old := &catalog.Relation{ColumnMeta: catalog.ColumnMetadata{HighWater: 3}}
	next := &catalog.Relation{Columns: []catalog.Column{{Name: "new", Usage: u}}, Attributes: []int32{4}, ColumnMeta: catalog.ColumnMetadata{HighWater: 4}}
	rows := []storage.RowVersion{{}, {}}
	projected, err := session.ProjectVersions(context.Background(), rows, old, next, map[int32]sqltype.Value{4: sqltype.Null(u.Type)})
	if err != nil || len(projected) != 2 {
		t.Fatal(projected, err)
	}
	for _, row := range projected {
		if len(row.Values) != 1 || !row.Values[0].IsNull() || row.Values[0].Type() != u.Type {
			t.Fatal(row)
		}
	}
	for _, row := range rows {
		if len(row.Values) != 0 {
			t.Fatal("source widened", row)
		}
	}
}

func TestAlterAddColumnCancellationDuringShapeTraversal(t *testing.T) {
	e := engine.New()
	u := enginetest.Usage(e, sqltype.Int4)
	one, err := sqltype.Integer(u.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	old := &catalog.Relation{Columns: []catalog.Column{{Name: "a", Usage: u}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	next := &catalog.Relation{Columns: []catalog.Column{{Name: "a", Usage: u}, {Name: "b", Usage: u}}, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}}
	rows := []storage.RowVersion{{Row: 1, Version: 1, Values: []sqltype.Value{one}}, {Row: 2, Version: 2, Values: []sqltype.Value{one}}, {Row: 3, Version: 3, Values: []sqltype.Value{one}}}
	projection, err := session.NewColumnProjection(old, next, map[int32]sqltype.Value{2: sqltype.Null(u.Type)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	traversal := &cancelAfterPreflightContext{Context: ctx, cancel: cancel}
	projected, err := projection.Versions(traversal, rows)
	if err != context.Canceled || projected != nil || traversal.checks != 3 {
		t.Fatal(projected, err, traversal.checks)
	}
	for i, row := range rows {
		if row.Row != ids.RowID(i+1) || len(row.Values) != 1 || row.Values[0] != one {
			t.Fatal("canceled traversal changed source", rows)
		}
	}
	healthy, err := projection.Versions(context.Background(), rows)
	if err != nil || len(healthy) != 3 {
		t.Fatal(healthy, err)
	}
}

// cancelAfterPreflightContext delegates to a real cancellable context. Its
// second successful Err observation cancels immediately after observing nil,
// deterministically placing cancellation between preflight and row preparation.
// It neither fabricates an error nor changes the underlying Done semantics.
type cancelAfterPreflightContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelAfterPreflightContext) Err() error {
	err := c.Context.Err()
	c.checks++
	if c.checks == 2 && err == nil {
		c.cancel()
	}
	return err
}
