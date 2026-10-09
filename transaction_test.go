package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

type transactionExecutor interface {
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func transactionExec(ctx context.Context, x transactionExecutor, q string, prepared bool, args ...any) (sql.Result, error) {
	if !prepared {
		return x.ExecContext(ctx, q, args...)
	}
	s, err := x.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.ExecContext(ctx, args...)
}

// A constraint witness must prove the actual API boundary independently:
// successful preparation, then the execution-time constraint diagnostic.
func transactionConstraintExec(t *testing.T, ctx context.Context, x transactionExecutor, q string, prepared bool) error {
	t.Helper()
	if !prepared {
		_, err := x.ExecContext(ctx, q)
		return err
	}
	stmt, err := x.PrepareContext(ctx, q)
	if err != nil {
		t.Fatalf("constraint INSERT must prepare successfully: %v", err)
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(ctx)
	return err
}

// transactionQuery keeps a prepared statement alive while its result is active.
// The caller closes rows before closing the statement, including on failure.
func transactionQuery(ctx context.Context, x transactionExecutor, q string, prepared bool) (*sql.Rows, func() error, error) {
	if !prepared {
		rows, err := x.QueryContext(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		return rows, rows.Close, nil
	}
	s, err := x.PrepareContext(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.QueryContext(ctx)
	if err != nil {
		s.Close()
		return nil, nil, err
	}
	return rows, func() error {
		rowErr := rows.Close()
		return errors.Join(rowErr, s.Close())
	}, nil
}
func transactionAffected(t *testing.T, ctx context.Context, x transactionExecutor, q string, prepared bool, want int64, args ...any) {
	t.Helper()
	r, err := transactionExec(ctx, x, q, prepared, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	count, err := r.RowsAffected()
	if err != nil || count != want {
		t.Fatal(q, count, err)
	}
}
func transactionValues(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" || types[0].ScanType() != reflect.TypeFor[int64]() {
		t.Fatal("metadata", types, err)
	}
	transactionRowValues(t, rows, want...)
}
func transactionRowValues(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	got := make([]int64, 0)
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		n, ok := v.(int64)
		if !ok {
			t.Fatalf("value type %T", v)
		}
		got = append(got, n)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if len(got) != len(want) {
		t.Fatalf("rows %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("rows %v want %v", got, want)
		}
	}
}
func transactionSelect(t *testing.T, ctx context.Context, x transactionExecutor, q string, prepared bool, want ...int64) {
	t.Helper()
	rows, closeQuery, err := transactionQuery(ctx, x, q, prepared)
	if err != nil {
		t.Fatal(q, err)
	}
	defer closeQuery()
	transactionValues(t, rows, want...)
	if err := closeQuery(); err != nil {
		t.Fatal(err)
	}
}
func transactionDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	var got *stuntgres.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic got %#v (%v) want %#v", got, err, want)
	}
}
func transactionOrdinary(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil || err.Error() != message {
		t.Fatalf("ordinary error %v want %q", err, message)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("fabricated SQLSTATE", err)
	}
}
func transactionConnections(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn) {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(3)
	ctx := context.Background()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return db, a, b
}
