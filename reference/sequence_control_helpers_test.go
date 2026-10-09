package reference_test

import (
	"context"
	"database/sql"
	"io"
	"stuntgres"
	"sync"
	"testing"
)

type sequenceFailingReader struct{ err error }

func (r sequenceFailingReader) Read([]byte) (int, error) { return 0, r.err }

type sequenceFailingWriter struct{ err error }

func (w sequenceFailingWriter) Write([]byte) (int, error) { return 0, w.err }

type sequenceGatedReader struct {
	reader  io.Reader
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *sequenceGatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started); <-r.release })
	return r.reader.Read(p)
}
func sequenceCoreConn(t *testing.T, ctx context.Context, db *sql.DB) *sql.Conn {
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
func sequenceNativeDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	d := controlsReferenceDiagnostic(want.Code, want.Message)
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
	referenceAssertDiagnostic(t, err, &d)
}

// Feed an already-open immutable cursor to the shipped consumption/recorder
// assertion; this never reissues the query after a catalog replacement.
type sequenceFrozenRows struct {
	referenceExecutor
	rows  *sql.Rows
	query string
}

func (f sequenceFrozenRows) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if q == f.query {
		return f.rows, nil
	}
	return f.referenceExecutor.QueryContext(ctx, q, args...)
}

// sequenceInt64Step retains primitive destinations on the same consumed row.
func sequenceInt64Step(step referenceStep) referenceStep {
	expectedRows := step.rows
	step.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
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
