package stuntgres

import (
	"context"
	"database/sql/driver"
	"io"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/parser"
)

type rows struct {
	ctx      context.Context
	columns  []parser.Column
	snapshot *engine.Snapshot
	index    int
	closed   bool
}

func newRows(ctx context.Context, r engine.Result) *rows {
	return &rows{ctx: ctx, columns: r.Columns, snapshot: r.Rows}
}
func (r *rows) Columns() []string {
	names := make([]string, len(r.columns))
	for i, c := range r.columns {
		names[i] = c.Name
	}
	return names
}
func (r *rows) Close() error { r.closed = true; r.snapshot = nil; return nil }
func (r *rows) Next(dst []driver.Value) error {
	if r.closed {
		return io.EOF
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if r.snapshot == nil || r.index >= r.snapshot.Len() {
		return io.EOF
	}
	r.snapshot.Values(r.index, dst)
	r.index++
	return nil
}
func (r *rows) ColumnTypeDatabaseTypeName(i int) string { return r.columns[i].Type.DatabaseType() }
func (r *rows) ColumnTypeScanType(i int) reflect.Type {
	switch r.columns[i].Type {
	case parser.Int4, parser.Int8:
		return reflect.TypeFor[int64]()
	case parser.Text:
		return reflect.TypeFor[string]()
	default:
		return reflect.TypeFor[bool]()
	}
}
func (r *rows) ColumnTypeNullable(i int) (bool, bool) { return r.columns[i].Nullable, true }

var _ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
var _ driver.RowsColumnTypeScanType = (*rows)(nil)
var _ driver.RowsColumnTypeNullable = (*rows)(nil)
