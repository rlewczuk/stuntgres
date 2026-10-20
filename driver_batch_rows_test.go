package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"stuntgres/internal/engine/exec"
	"testing"
)

func TestDriverBatchResultSetMechanics(t *testing.T) {
	c := adapterMechanicsConn(t, NewDriver(), "batch-result-mechanics")
	if _, err := c.ExecContext(context.Background(), "CREATE TABLE zero_width(); INSERT INTO zero_width DEFAULT VALUES; INSERT INTO zero_width DEFAULT VALUES", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := c.QueryContext(context.Background(), "SELECT 1::int4 AS first UNION ALL SELECT 2::int4; SELECT 'x'::text AS label,true AS flag; SELECT FROM zero_width", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := raw.(*rows)
	saved := r.Columns()
	if !slices.Equal(saved, []string{"first"}) || r.ColumnTypeDatabaseTypeName(0) != "INT4" || !r.HasNextResultSet() {
		t.Fatal("first metadata", saved)
	}
	if err := r.NextResultSet(); err != nil {
		t.Fatal("skip unread", err)
	}
	if !slices.Equal(r.Columns(), []string{"label", "flag"}) || r.ColumnTypeDatabaseTypeName(0) != "TEXT" || r.ColumnTypeDatabaseTypeName(1) != "BOOL" {
		t.Fatal("second metadata", r.Columns())
	}
	dst := make([]driver.Value, 2)
	if err := r.Next(dst); err != nil || dst[0] != "x" || dst[1] != true {
		t.Fatal(dst, err)
	}
	for range 2 {
		if err := r.Next(dst); err != io.EOF {
			t.Fatal("stable current EOF", err)
		}
	}
	if err := r.NextResultSet(); err != nil {
		t.Fatal(err)
	}
	if len(r.Columns()) != 0 {
		t.Fatal("zero-width schema", r.Columns())
	}
	for range 2 {
		if err := r.Next(nil); err != nil {
			t.Fatal("zero-width occurrence", err)
		}
	}
	if err := r.Next(nil); err != io.EOF || r.HasNextResultSet() {
		t.Fatal("final EOF", err)
	}
	if err := r.NextResultSet(); err != io.EOF {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if r.cursor != nil || r.pending != nil || r.scratch != nil || !slices.Equal(saved, []string{"first"}) {
		t.Fatal("ownership/cleanup", saved)
	}
	if _, err := c.ExecContext(context.Background(), "SELECT 3", nil); err != nil {
		t.Fatal("physical owner reuse", err)
	}
}

type batchCloseObserver struct {
	exec.RowResult
	calls   int
	failure error
}

func (r *batchCloseObserver) Close() error {
	r.calls++
	err := r.RowResult.Close()
	if err != nil {
		return err
	}
	return r.failure
}
func batchObservedCursor(t *testing.T, c *conn, q string, failure error) *batchCloseObserver {
	t.Helper()
	raw, err := c.QueryContext(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := raw.(*rows)
	cursor := r.cursor
	r.cursor = nil
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	return &batchCloseObserver{RowResult: cursor, failure: failure}
}
func TestDriverBatchPrimaryCleanupAndCancellation(t *testing.T) {
	for _, primary := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup", true: "primary"}[primary], func(t *testing.T) {
			c := adapterMechanicsConn(t, NewDriver(), "batch-close-mechanics")
			cleanup := errors.New("observed cleanup failure")
			first := batchObservedCursor(t, c, "SELECT 1", cleanup)
			second := batchObservedCursor(t, c, "SELECT 2", nil)
			var terminal error
			if primary {
				terminal = errors.New("already executed terminal error")
			}
			r := newBatchRows(context.Background(), []exec.RowResult{first, second}, terminal)
			want := cleanup
			if primary {
				want = terminal
			}
			if err := r.NextResultSet(); err != want {
				t.Fatal("original beats cleanup", err, want)
			}
			for range 2 {
				if err := r.Close(); err != want {
					t.Fatal("sticky Close", err, want)
				}
			}
			if first.calls != 1 || second.calls != 1 || r.pending != nil || r.cursor != nil || r.scratch != nil {
				t.Fatal("every output exactly once", first.calls, second.calls)
			}
		})
	}
	c := adapterMechanicsConn(t, NewDriver(), "batch-cancel-mechanics")
	ctx, cancel := context.WithCancel(context.Background())
	raw, err := c.QueryContext(ctx, "SELECT 1 AS a; SELECT 2 AS b", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := raw.(*rows)
	saved := r.Columns()
	cancel()
	if err := r.Next(make([]driver.Value, 1)); err != context.Canceled {
		t.Fatal("ordinary caller cancellation", err)
	}
	for range 2 {
		if err := r.Next(make([]driver.Value, 1)); err != context.Canceled {
			t.Fatal("sticky iteration cancellation", err)
		}
		if err := r.NextResultSet(); err != context.Canceled {
			t.Fatal("sticky result-set cancellation", err)
		}
	}
	for range 2 {
		if err := r.Close(); err != nil {
			t.Fatal("successful canceled-iteration cleanup", err)
		}
	}
	if r.pending != nil || r.cursor != nil || r.scratch != nil || !slices.Equal(saved, []string{"a"}) {
		t.Fatal("canceled output ownership")
	}
	ctx, cancel = context.WithCancel(context.Background())
	raw, err = c.QueryContext(ctx, "SELECT 1; SELECT missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	r = raw.(*rows)
	cancel()
	err = r.Next(make([]driver.Value, 1))
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "42703" {
		t.Fatal("executed SQL error precedes caller cancellation", err)
	}
	for range 2 {
		if closeErr := r.Close(); closeErr != err {
			t.Fatal("executed batch error survives Close", closeErr, err)
		}
	}
}
func TestDriverBatchDestinationWidthAndEagerFailure(t *testing.T) {
	c := adapterMechanicsConn(t, NewDriver(), "batch-width-mechanics")
	raw, err := c.QueryContext(context.Background(), "SELECT 1 AS a; SELECT 2 AS b", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := raw.(*rows)
	err = r.Next(nil)
	if err == nil || err.Error() != "stuntgres: invalid driver destination width" {
		t.Fatal("exact destination contract", err)
	}
	if r.pending != nil || r.cursor != nil || r.scratch != nil || r.Close() != err {
		t.Fatal("width failure released every set")
	}
	raw, err = c.QueryContext(context.Background(), "SELECT 1 AS a WHERE false; SELECT missing", nil)
	if err != nil {
		t.Fatal("completed empty set retained", err)
	}
	r = raw.(*rows)
	if !slices.Equal(r.Columns(), []string{"a"}) {
		t.Fatal(r.Columns())
	}
	err = r.Next(make([]driver.Value, 1))
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "42703" {
		t.Fatal("original eager failure", err)
	}
	if r.NextResultSet() != err || r.Close() != err {
		t.Fatal("sticky terminal boundary")
	}
}
