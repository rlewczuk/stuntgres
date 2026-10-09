package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func referenceRowExecute(ctx context.Context, x referenceExecutor, query string, prepared bool) relationOutcome {
	var outcome relationOutcome
	if prepared {
		outcome.stmt, outcome.err = x.PrepareContext(ctx, query)
		if outcome.err == nil {
			outcome.result, outcome.err = outcome.stmt.ExecContext(ctx)
		}
		if outcome.stmt != nil {
			outcome.stmt.Close()
			outcome.stmt = nil
		}
	} else {
		outcome.result, outcome.err = x.ExecContext(ctx, query)
	}
	return outcome
}
func referenceRowAsync(t *testing.T, ctx context.Context, x referenceExecutor, query string, prepared, selectRows bool, raw ...referenceRawBackend) <-chan relationOutcome {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	done := make(chan relationOutcome, 1)
	backend := referenceRawLegacy
	if len(raw) != 0 {
		backend = raw[0]
	}
	go func() {
		var outcome relationOutcome
		if selectRows {
			outcome = referenceRowQuery(bounded, x, query, prepared, backend)
		} else {
			outcome = referenceRowExecute(bounded, x, query, prepared)
		}
		done <- outcome
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case outcome := <-done:
			if outcome.rows != nil {
				outcome.rows.Close()
			}
			if outcome.stmt != nil {
				outcome.stmt.Close()
			}
		case <-time.After(5 * time.Second):
			t.Error("row-lock asynchronous operation did not join")
		}
	})
	return done
}
func referenceRowAffected(t *testing.T, ctx context.Context, x referenceExecutor, query string, prepared bool, count int64) {
	t.Helper()
	outcome := referenceRowExecute(ctx, x, query, prepared)
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	referenceLockAffected(t, outcome.result, count)
}

type relationOutcome struct {
	rows   *sql.Rows
	stmt   *sql.Stmt
	result sql.Result
	err    error
	// Optional immutable fixture provenance for retained-cursor descriptors.
	rawBackend referenceRawBackend
}

func referenceLockResult(t *testing.T, done <-chan relationOutcome) relationOutcome {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("lock command did not finish")
		return relationOutcome{}
	}
}
func referenceLockAffected(t *testing.T, r sql.Result, want int64) {
	t.Helper()
	if r == nil {
		t.Fatal("missing result")
	}
	n, err := r.RowsAffected()
	if err != nil || n != want {
		t.Fatal(n, err, "want", want)
	}
}
func referenceLockConnections(t *testing.T, ctx context.Context, db *sql.DB) (*sql.Conn, *sql.Conn) {
	t.Helper()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		a.ExecContext(ctx, "ROLLBACK")
		a.Close()
	})
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		b.ExecContext(ctx, "ROLLBACK")
		b.Close()
	})
	return a, b
}
func referenceLockExec(t *testing.T, ctx context.Context, x referenceExecutor, q string, n int64) {
	t.Helper()
	r, err := x.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	referenceLockAffected(t, r, n)
}

// Tuple arbitration commonly waits on transactionid, not a relation lock.
func referenceRowWait(t *testing.T, ctx context.Context, pid, holder int) {
	t.Helper()
	for {
		var waiting bool
		err := referenceAdmin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock') AND $2=ANY(pg_blocking_pids($1))`, pid, holder).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
}
func referenceRowQuery(ctx context.Context, x referenceExecutor, query string, prepared bool, raw ...referenceRawBackend) relationOutcome {
	var outcome relationOutcome
	if len(raw) != 0 {
		outcome.rawBackend = raw[0]
	}
	if prepared {
		outcome.stmt, outcome.err = x.PrepareContext(ctx, query)
		if outcome.err == nil {
			outcome.rows, outcome.err = outcome.stmt.QueryContext(ctx)
		}
	} else {
		outcome.rows, outcome.err = x.QueryContext(ctx, query)
	}
	return outcome
}
func referenceRowValues(t *testing.T, outcome relationOutcome, want ...int64) {
	t.Helper()
	if outcome.stmt != nil {
		defer outcome.stmt.Close()
	}
	if outcome.rows != nil {
		defer outcome.rows.Close()
	}
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.rows == nil {
		t.Fatal("missing cursor")
	}
	columns, err := outcome.rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != "v" {
		t.Fatal(columns, err)
	}
	types, err := outcome.rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal(types, err)
	}
	if outcome.rawBackend != referenceRawLegacy {
		scan := reflect.TypeFor[int64]()
		if outcome.rawBackend == referenceRawPostgres {
			scan = reflect.TypeFor[int32]()
		}
		if types[0].ScanType() != scan {
			t.Fatalf("INT4 ScanType %v, want %v", types[0].ScanType(), scan)
		}
	}
	var got []int64
	for outcome.rows.Next() {
		var value any
		if err := outcome.rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		v, ok := value.(int64)
		if !ok {
			t.Fatalf("raw INT4 payload %T, want int64", value)
		}
		got = append(got, v)
	}
	if err := outcome.rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal(got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatal(got, want)
		}
	}
	if err := outcome.rows.Close(); err != nil {
		t.Fatal(err)
	}
	if outcome.stmt != nil {
		if err := outcome.stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := outcome.rows.Close(); err != nil {
		t.Fatal(err)
	}
	if outcome.stmt != nil {
		if err := outcome.stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
func referenceRowPIDs(t *testing.T, ctx context.Context, a, b *sql.Conn) (int, int) {
	t.Helper()
	var ap, bp int
	if err := a.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
		t.Fatal(err)
	}
	if err := b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
		t.Fatal(err)
	}
	return ap, bp
}

// A positive native is_called transition proves volatile evaluation used the
// original pre-release candidate. It is not a guessed timeout or a public lock
// hook; exact native queued-request ownership is proved by engine tests.
func referenceLimitSequenceCapture(t *testing.T, parent context.Context, holder *sql.Tx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	for {
		var called bool
		if err := holder.QueryRowContext(ctx, "SELECT is_called FROM s").Scan(&called); err != nil {
			t.Fatal("original volatile candidate was not observed", err)
		}
		if called {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
}
