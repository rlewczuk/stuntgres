package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Like HeldStatement, this routes the shared metadata/value assertions through
// a genuine already-returned result, without issuing the SQL a second time.
type fdwHeldResult struct {
	fixture.Executor
	query    string
	rows     *sql.Rows
	recorder *fixture.Recorder
}

func (h fdwHeldResult) QueryContext(_ context.Context, q string, _ ...any) (*sql.Rows, error) {
	if q != h.query {
		return nil, fmt.Errorf("held result query differs: %q", q)
	}
	return h.rows, nil
}
func fdwHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) fdwHeldResult {
	t.Helper()
	// Each held result retains its own operation history: later execution of
	// identical SQL must not replace the record checked when this batch is read.
	recorder := &fixture.Recorder{RawBackend: b.Recorder.RawBackend}
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, recorder)
	rows, err := c.QueryContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	t.Cleanup(func() { rows.Close() })
	return fdwHeldResult{Executor: c, query: q, rows: rows, recorder: recorder}
}
func fdwReleased(t *testing.T, r *fdwSQLReader, before fdwReaderState, begins int) fdwReaderState {
	t.Helper()
	after := r.snapshot()
	if after.openRows != 0 || after.openCursors != 0 || after.openPools != 0 || after.begins-before.begins != begins || after.ends-before.ends != begins {
		t.Fatalf("reader ownership before=%+v after=%+v want begins/ends delta=%d", before, after, begins)
	}
	return after
}
func fdwHostQueryError(t *testing.T, ctx context.Context, c *sql.Conn, q string, want error) error {
	t.Helper()
	rows, err := c.QueryContext(ctx, q)
	if rows != nil {
		rows.Close()
		t.Fatal("failed eager query published rows", q)
	}
	if !errors.Is(err, want) {
		t.Fatalf("%s got %v want %v", q, err, want)
	}
	return err
}

// Reuse one mutable byte buffer for real SQL TEXT cells, including across
// source generations, and invalidate it during real End. This changes only
// transport ownership, never the independently produced source values.
func fdwBorrowedBytes(r *fdwSQLReader) func() {
	scratch := make([]byte, 0, 64)
	r.mu.Lock()
	r.rowTransform = func(_ context.Context, row stuntgres.ForeignRow) (stuntgres.ForeignRow, error) {
		for i, value := range row {
			if text, ok := value.(string); ok {
				scratch = append(scratch[:0], text...)
				row[i] = scratch
				break
			}
		}
		return row, nil
	}
	r.mu.Unlock()
	r.setHook(func(ctx context.Context, method string) error {
		if method == "EndForeignScan" {
			for i := range scratch {
				scratch[i] = '!'
			}
		}
		return ctx.Err()
	})
	return func() { r.setHook(nil); r.mu.Lock(); r.rowTransform = nil; r.mu.Unlock() }
}
