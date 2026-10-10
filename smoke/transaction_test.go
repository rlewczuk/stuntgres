package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"stuntgres"
)

type transactionExecutor interface {
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func transactionExecute(ctx context.Context, x transactionExecutor, q string, prepared bool, args ...any) (sql.Result, error) {
	if !prepared {
		return x.ExecContext(ctx, q, args...)
	}
	stmt, err := x.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return stmt.ExecContext(ctx, args...)
}
func transactionAffected(ctx context.Context, x transactionExecutor, q string, prepared bool, want int64, args ...any) error {
	r, err := transactionExecute(ctx, x, q, prepared, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", q, err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("%s affected %d want %d", q, n, want)
	}
	return nil
}
func transactionValues(rows *sql.Rows, want ...int64) error {
	return transactionTypedValues(rows, "v", "INT4", want...)
}
func transactionTypedValues(rows *sql.Rows, name, typ string, want ...int64) error {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(columns, []string{name}) || len(types) != 1 || types[0].DatabaseTypeName() != typ || types[0].ScanType() != reflect.TypeFor[int64]() {
		return fmt.Errorf("wrong metadata %v %v", columns, types)
	}
	got := make([]int64, 0)
	for rows.Next() {
		var v any
		if err = rows.Scan(&v); err != nil {
			return err
		}
		n, ok := v.(int64)
		if !ok {
			return fmt.Errorf("value has type %T", v)
		}
		got = append(got, n)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("rows %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("rows %v want %v", got, want)
		}
	}
	return nil
}
func transactionSelectValues(ctx context.Context, x transactionExecutor, q string, prepared bool, want ...int64) error {
	return transactionSelectTypedValues(ctx, x, q, prepared, "v", "INT4", want...)
}
func transactionSelectTypedValues(ctx context.Context, x transactionExecutor, q string, prepared bool, name, typ string, want ...int64) error {
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := x.PrepareContext(ctx, q)
		if e != nil {
			return e
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(ctx)
	} else {
		rows, err = x.QueryContext(ctx, q)
	}
	if err != nil {
		return err
	}
	return transactionTypedValues(rows, name, typ, want...)
}
func transactionDiagnostic(err error, want stuntgres.Error) error {
	var got *stuntgres.Error
	if !errors.As(err, &got) || *got != want {
		return fmt.Errorf("diagnostic %#v (%v) want %#v", got, err, want)
	}
	return nil
}
