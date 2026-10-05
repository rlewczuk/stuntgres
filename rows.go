package stuntgres

import (
	"context"
	"database/sql/driver"
	"io"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
)

type rows struct {
	ctx      context.Context
	columns  []engine.ColumnDescriptor
	snapshot *engine.Snapshot
	index    int
	closed   bool
	scratch []sqltype.Value
}

func newRows(ctx context.Context, r engine.Result) *rows {
	return &rows{ctx: ctx, columns: r.Columns, snapshot: r.Rows, scratch:make([]sqltype.Value,len(r.Columns))}
}
func (r *rows) Columns() []string {
	names := make([]string, len(r.columns))
	for i, c := range r.columns {
		names[i] = c.Name
	}
	return names
}
func (r *rows) Close() error { r.closed = true; r.snapshot = nil; r.scratch=nil; return nil }
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
	r.snapshot.Values(r.index, r.scratch)
	for i,v := range r.scratch {
		value, err := exportValue(r.ctx,v)
		if err != nil { return err }
		dst[i] = value
	}
	r.index++
	return nil
}
func (r *rows) ColumnTypeDatabaseTypeName(i int) string { return r.columns[i].Usage.Type.DatabaseName() }
func (r *rows) ColumnTypeScanType(i int) reflect.Type { return scanType(r.columns[i].Usage.Type) }
func (r *rows) ColumnTypeNullable(i int) (bool, bool) { return r.columns[i].Nullable,r.columns[i].NullabilityKnown }

var _ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
var _ driver.RowsColumnTypeScanType = (*rows)(nil)
var _ driver.RowsColumnTypeNullable = (*rows)(nil)
