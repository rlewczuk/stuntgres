package stuntgres

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
)

type rows struct {
	ctx                context.Context
	columns            []engine.ColumnDescriptor
	cursor             engine.RowResult
	closed, exhausted  bool
	terminal, closeErr error
	scratch            []sqltype.Value
}

func newRows(ctx context.Context, r engine.Result) *rows {
	var schema []engine.ColumnDescriptor
	if r.Rows != nil {
		schema = r.Rows.Schema()
	}
	return &rows{ctx: ctx, columns: schema, cursor: r.Rows, scratch: make([]sqltype.Value, len(schema))}
}
func (r *rows) Columns() []string {
	names := make([]string, len(r.columns))
	for i, c := range r.columns {
		names[i] = c.Name
	}
	return names
}
func (r *rows) release() error {
	var err error
	if r.cursor != nil {
		err = r.cursor.Close()
		r.cursor = nil
		r.closeErr = err
	}
	r.scratch = nil
	return r.closeErr
}
func (r *rows) Close() error {
	if !r.closed {
		r.closeErr = r.release()
		r.closed = true
		r.terminal = nil
	}
	return r.closeErr
}
func (r *rows) fail(err error) error {
	r.terminal = err
	r.closeErr = r.release()
	return err
}
func (r *rows) Next(dst []driver.Value) error {
	if r.closed || r.exhausted {
		return io.EOF
	}
	if r.terminal != nil {
		return r.terminal
	}
	if err := r.ctx.Err(); err != nil {
		return r.fail(err)
	}
	if len(dst) != len(r.columns) {
		return r.fail(fmt.Errorf("stuntgres: invalid driver destination width"))
	}
	if r.cursor == nil {
		r.exhausted = true
		return io.EOF
	}
	present, err := r.cursor.Next(r.ctx, r.scratch)
	if err != nil {
		return r.fail(err)
	}
	if !present {
		if err := r.release(); err != nil {
			return r.fail(err)
		}
		r.exhausted = true
		return io.EOF
	}
	for i, v := range r.scratch {
		value, err := exportValue(r.ctx, v)
		if err != nil {
			return r.fail(err)
		}
		dst[i] = value
	}
	return nil
}
func (r *rows) ColumnTypeDatabaseTypeName(i int) string {
	return r.columns[i].Usage.Type.DatabaseName()
}
func (r *rows) ColumnTypeScanType(i int) reflect.Type { return scanType(r.columns[i].Usage.Type) }
func (r *rows) ColumnTypeNullable(i int) (bool, bool) {
	return r.columns[i].Nullable, r.columns[i].NullabilityKnown
}

var _ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
var _ driver.RowsColumnTypeScanType = (*rows)(nil)
var _ driver.RowsColumnTypeNullable = (*rows)(nil)
