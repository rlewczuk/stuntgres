package fixture

import (
	"context"
	"database/sql"
	"io"
	"stuntgres"
	"sync"
	"testing"
)

type SequenceFailingReader struct{ Err error }

func (r SequenceFailingReader) Read([]byte) (int, error) { return 0, r.Err }

type SequenceFailingWriter struct{ Err error }

func (w SequenceFailingWriter) Write([]byte) (int, error) { return 0, w.Err }

type SequenceGatedReader struct {
	Reader  io.Reader
	Started chan struct{}
	Release chan struct{}
	once    sync.Once
}

func (r *SequenceGatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.Started); <-r.Release })
	return r.Reader.Read(p)
}
func SequenceCoreConn(t *testing.T, ctx context.Context, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil && err != sql.ErrConnDone {
			t.Error(err)
		}
	})
	return c
}

// Preserve complete public native records through the fixture's diagnostic assertion.
func SequenceNativeDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	d := ControlsReferenceDiagnostic(want.Code, want.Message)
	d.Detail = want.Detail
	d.Hint = want.Hint
	d.Position = want.Position
	d.Context = want.Context
	d.InternalPosition = want.InternalPosition
	d.InternalQuery = want.InternalQuery
	d.Schema = want.Schema
	d.Table = want.Table
	d.Column = want.Column
	d.DataType = want.DataType
	d.Constraint = want.Constraint
	AssertDiagnostic(t, err, &d)
}

// Feed an already-open immutable cursor to the shipped consumption/recorder
// assertion; this never reissues the query after a catalog replacement.
type SequenceFrozenRows struct {
	Executor
	Rows  *sql.Rows
	Query string
}

func (f SequenceFrozenRows) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if q == f.Query {
		return f.Rows, nil
	}
	return f.Executor.QueryContext(ctx, q, args...)
}

// SequenceInt64Step retains primitive destinations on the same consumed row.
func SequenceInt64Step(step Step) Step {
	expectedRows := step.Rows
	step.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
		t.Helper()
		if rowIndex >= len(expectedRows) {
			t.Fatalf("unexpected integer row %d", rowIndex)
		}
		expected := expectedRows[rowIndex]
		values := make([]int64, len(expected))
		destinations := make([]any, len(expected))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			want, ok := expected[i].(int64)
			if !ok || value != want {
				t.Fatalf("integer row %d column %d: got %d, want %v", rowIndex, i, value, expected[i])
			}
		}
	}
	return step
}
