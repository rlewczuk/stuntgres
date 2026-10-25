package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

type rows struct {
	ctx                context.Context
	columns            []catalog.Column
	cursor             exec.RowResult
	closed, exhausted  bool
	terminal, closeErr error
	scratch            []sqltype.Value
	pending            []exec.RowResult
	batchError         error
}

func newRows(ctx context.Context, r core.Result) *rows {
	var schema []catalog.Column
	if r.Rows != nil {
		schema = r.Rows.Schema()
	}
	return &rows{ctx: ctx, columns: schema, cursor: r.Rows, scratch: make([]sqltype.Value, len(schema))}
}
func newBatchRows(ctx context.Context, sets []exec.RowResult, terminal error) *rows {
	schema := sets[0].Schema()
	width := len(schema)
	for _, set := range sets[1:] {
		if n := len(set.Schema()); n > width {
			width = n
		}
	}
	r := &rows{ctx: ctx, columns: schema, cursor: sets[0], scratch: make([]sqltype.Value, len(schema), width)}
	r.pending = sets[1:]
	r.batchError = terminal
	return r
}
func (r *rows) HasNextResultSet() bool { return !r.closed && r.terminal == nil && len(r.pending) != 0 }
func (r *rows) NextResultSet() error {
	if r.closed {
		return io.EOF
	}
	if r.terminal != nil {
		return r.terminal
	}
	if err := r.ctx.Err(); err != nil {
		return r.fail(err)
	}
	if err := r.release(); err != nil {
		return r.fail(err)
	}
	if len(r.pending) == 0 {
		if r.batchError != nil {
			return r.fail(r.batchError)
		}
		return io.EOF
	}
	r.cursor = r.pending[0]
	r.pending[0] = nil
	r.pending = r.pending[1:]
	r.columns = r.cursor.Schema()
	r.scratch = r.scratch[:len(r.columns)]
	r.exhausted = false
	return nil
}
func (r *rows) Columns() []string {
	names := make([]string, len(r.columns))
	for i, c := range r.columns {
		names[i] = c.Name
	}
	return names
}
func (r *rows) release() error {
	if r.cursor != nil {
		err := r.cursor.Close()
		r.cursor = nil
		if r.closeErr == nil {
			r.closeErr = err
		}
	}
	clear(r.scratch)
	return r.closeErr
}
func (r *rows) releaseAll() {
	r.release()
	for _, cursor := range r.pending {
		if err := cursor.Close(); r.closeErr == nil {
			r.closeErr = err
		}
	}
	r.pending = nil
	r.scratch = nil
}
func (r *rows) Close() error {
	if !r.closed {
		r.releaseAll()
		r.closed = true
	}
	if r.batchError != nil {
		return r.batchError
	}
	// Caller cancellation belongs to iteration. Once Next has observed it,
	// successful explicit cleanup must not manufacture a new Close failure.
	if r.terminal != nil && !errors.Is(r.terminal, context.Canceled) && !errors.Is(r.terminal, context.DeadlineExceeded) {
		return r.terminal
	}
	return r.closeErr
}
func (r *rows) fail(err error) error {
	if r.batchError != nil {
		err = r.batchError
	}
	r.terminal = err
	r.releaseAll()
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
		if len(r.pending) == 0 && r.batchError != nil {
			return r.fail(r.batchError)
		}
		r.exhausted = true
		return io.EOF
	}
	for i, v := range r.scratch {
		var value driver.Value
		var err error
		if !v.IsNull() && v.Type().BaseType().ID() == sqltype.Regclass {
			if v.Type().IsDomain() {
				v, err = v.BaseValue()
				if err != nil {
					return r.fail(err)
				}
			}
			value, err = r.cursor.RegclassText(v)
		} else {
			value, err = exportValue(r.ctx, v, r.cursor.OutputEnvironment())
		}
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
func (r *rows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	return r.columns[i].Usage.DecimalSize()
}
func (r *rows) ColumnTypeLength(i int) (int64, bool) { return r.columns[i].Usage.Length() }

var _ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
var _ driver.RowsColumnTypeScanType = (*rows)(nil)
var _ driver.RowsColumnTypeNullable = (*rows)(nil)
var _ driver.RowsColumnTypePrecisionScale = (*rows)(nil)
var _ driver.RowsColumnTypeLength = (*rows)(nil)
var _ driver.RowsNextResultSet = (*rows)(nil)
