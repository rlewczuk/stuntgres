//go:build reference

package stuntgres_test

import (
	"context"
	"database/sql/driver"
	"io"
	"sync"

	"github.com/ngrok/sqlmw"
)

type referenceRecorderKey struct{}
type referenceRowsKey struct{}

type referenceOperation struct {
	SQL      string
	Args     []driver.NamedValue
	Affected int64
	Err      error
	Columns  []string
	Rows     [][]driver.Value
	Closed   bool
	CloseErr error
}

type referenceRecorder struct {
	mu         sync.Mutex
	operations []*referenceOperation
}

func referenceCopyValue(v driver.Value) driver.Value {
	if b, ok := v.([]byte); ok {
		return append([]byte(nil), b...)
	}
	return v
}

func (r *referenceRecorder) begin(query string, args []driver.NamedValue) *referenceOperation {
	op := &referenceOperation{SQL: query, Args: make([]driver.NamedValue, len(args))}
	for i, arg := range args {
		op.Args[i] = arg
		op.Args[i].Value = referenceCopyValue(arg.Value)
	}
	r.mu.Lock()
	r.operations = append(r.operations, op)
	r.mu.Unlock()
	return op
}

func referenceBegin(ctx context.Context, query string, args []driver.NamedValue) (*referenceRecorder, *referenceOperation) {
	r, _ := ctx.Value(referenceRecorderKey{}).(*referenceRecorder)
	if r == nil {
		return nil, nil
	}
	return r, r.begin(query, args)
}

func referenceRecordExec(r *referenceRecorder, op *referenceOperation, result driver.Result, err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	op.Err = err
	if err == nil && result != nil {
		op.Affected, op.Err = result.RowsAffected()
	}
}

func referenceRecordQuery(ctx context.Context, r *referenceRecorder, op *referenceOperation, rows driver.Rows, err error) context.Context {
	if r == nil {
		return ctx
	}
	r.mu.Lock()
	op.Err = err
	if err == nil && rows != nil {
		op.Columns = append([]string(nil), rows.Columns()...)
	}
	r.mu.Unlock()
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, referenceRowsKey{}, op)
}

type referenceInterceptor struct{ sqlmw.NullInterceptor }

func (referenceInterceptor) ConnExecContext(ctx context.Context, conn driver.ExecerContext, query string, args []driver.NamedValue) (driver.Result, error) {
	r, op := referenceBegin(ctx, query, args)
	result, err := conn.ExecContext(ctx, query, args)
	referenceRecordExec(r, op, result, err)
	return result, err
}
func (referenceInterceptor) StmtExecContext(ctx context.Context, stmt driver.StmtExecContext, query string, args []driver.NamedValue) (driver.Result, error) {
	r, op := referenceBegin(ctx, query, args)
	result, err := stmt.ExecContext(ctx, args)
	referenceRecordExec(r, op, result, err)
	return result, err
}
func (referenceInterceptor) ConnQueryContext(ctx context.Context, conn driver.QueryerContext, query string, args []driver.NamedValue) (context.Context, driver.Rows, error) {
	r, op := referenceBegin(ctx, query, args)
	rows, err := conn.QueryContext(ctx, query, args)
	return referenceRecordQuery(ctx, r, op, rows, err), rows, err
}
func (referenceInterceptor) StmtQueryContext(ctx context.Context, stmt driver.StmtQueryContext, query string, args []driver.NamedValue) (context.Context, driver.Rows, error) {
	r, op := referenceBegin(ctx, query, args)
	rows, err := stmt.QueryContext(ctx, args)
	return referenceRecordQuery(ctx, r, op, rows, err), rows, err
}
func (referenceInterceptor) RowsNext(ctx context.Context, rows driver.Rows, dest []driver.Value) error {
	err := rows.Next(dest)
	r, _ := ctx.Value(referenceRecorderKey{}).(*referenceRecorder)
	op, _ := ctx.Value(referenceRowsKey{}).(*referenceOperation)
	if r != nil && op != nil {
		r.mu.Lock()
		if err == nil {
			values := make([]driver.Value, len(dest))
			for i, v := range dest {
				values[i] = referenceCopyValue(v)
			}
			op.Rows = append(op.Rows, values)
		} else if err != io.EOF {
			op.Err = err
		}
		r.mu.Unlock()
	}
	return err
}
func (referenceInterceptor) RowsClose(ctx context.Context, rows driver.Rows) error {
	err := rows.Close()
	r, _ := ctx.Value(referenceRecorderKey{}).(*referenceRecorder)
	op, _ := ctx.Value(referenceRowsKey{}).(*referenceOperation)
	if r != nil && op != nil {
		r.mu.Lock()
		op.Closed = true
		op.CloseErr = err
		r.mu.Unlock()
	}
	return err
}
