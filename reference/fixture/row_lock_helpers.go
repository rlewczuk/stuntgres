package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func RowExecute(ctx context.Context, x Executor, query string, prepared bool) RelationOutcome {
	var outcome RelationOutcome
	if prepared {
		outcome.Stmt, outcome.Err = x.PrepareContext(ctx, query)
		if outcome.Err == nil {
			outcome.Result, outcome.Err = outcome.Stmt.ExecContext(ctx)
		}
		if outcome.Stmt != nil {
			outcome.Stmt.Close()
			outcome.Stmt = nil
		}
	} else {
		outcome.Result, outcome.Err = x.ExecContext(ctx, query)
	}
	return outcome
}
func RowAsync(t *testing.T, ctx context.Context, x Executor, query string, prepared, selectRows bool, raw ...referenceRawBackend) <-chan RelationOutcome {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	done := make(chan RelationOutcome, 1)
	backend := RawLegacy
	if len(raw) != 0 {
		backend = raw[0]
	}
	go func() {
		var outcome RelationOutcome
		if selectRows {
			outcome = RowQuery(bounded, x, query, prepared, backend)
		} else {
			outcome = RowExecute(bounded, x, query, prepared)
		}
		done <- outcome
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case outcome := <-done:
			if outcome.Rows != nil {
				outcome.Rows.Close()
			}
			if outcome.Stmt != nil {
				outcome.Stmt.Close()
			}
		case <-time.After(5 * time.Second):
			t.Error("row-lock asynchronous operation did not join")
		}
	})
	return done
}
func RowAffected(t *testing.T, ctx context.Context, x Executor, query string, prepared bool, count int64) {
	t.Helper()
	outcome := RowExecute(ctx, x, query, prepared)
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	LockAffected(t, outcome.Result, count)
}

type RelationOutcome struct {
	Rows   *sql.Rows
	Stmt   *sql.Stmt
	Result sql.Result
	Err    error
	// Optional immutable fixture provenance for retained-cursor descriptors.
	RawBackend referenceRawBackend
}

func LockResult(t *testing.T, done <-chan RelationOutcome, limit ...time.Duration) RelationOutcome {
	t.Helper()
	timeout := 5 * time.Second
	if len(limit) != 0 {
		timeout = limit[0]
	}
	select {
	case r := <-done:
		return r
	case <-time.After(timeout):
		t.Fatal("lock command did not finish")
		return RelationOutcome{}
	}
}
func LockAffected(t *testing.T, r sql.Result, want int64) {
	t.Helper()
	if r == nil {
		t.Fatal("missing result")
	}
	n, err := r.RowsAffected()
	if err != nil || n != want {
		t.Fatal(n, err, "want", want)
	}
}
func LockConnections(t *testing.T, ctx context.Context, db *sql.DB) (*sql.Conn, *sql.Conn) {
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
func LockExec(t *testing.T, ctx context.Context, x Executor, q string, n int64) {
	t.Helper()
	r, err := x.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	LockAffected(t, r, n)
}

// Tuple arbitration commonly waits on transactionid, not a relation lock.
func RowWait(t *testing.T, ctx context.Context, pid, holder int) {
	t.Helper()
	for {
		var waiting bool
		err := Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock') AND $2=ANY(pg_blocking_pids($1))`, pid, holder).Scan(&waiting)
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
func RowQuery(ctx context.Context, x Executor, query string, prepared bool, raw ...referenceRawBackend) RelationOutcome {
	var outcome RelationOutcome
	if len(raw) != 0 {
		outcome.RawBackend = raw[0]
	}
	if prepared {
		outcome.Stmt, outcome.Err = x.PrepareContext(ctx, query)
		if outcome.Err == nil {
			outcome.Rows, outcome.Err = outcome.Stmt.QueryContext(ctx)
		}
	} else {
		outcome.Rows, outcome.Err = x.QueryContext(ctx, query)
	}
	return outcome
}
func RowValues(t *testing.T, outcome RelationOutcome, want ...int64) {
	t.Helper()
	if outcome.Stmt != nil {
		defer outcome.Stmt.Close()
	}
	if outcome.Rows != nil {
		defer outcome.Rows.Close()
	}
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	if outcome.Rows == nil {
		t.Fatal("missing cursor")
	}
	columns, err := outcome.Rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != "v" {
		t.Fatal(columns, err)
	}
	types, err := outcome.Rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal(types, err)
	}
	if outcome.RawBackend != RawLegacy {
		scan := reflect.TypeFor[int64]()
		if outcome.RawBackend == RawPostgres {
			scan = reflect.TypeFor[int32]()
		}
		if types[0].ScanType() != scan {
			t.Fatalf("INT4 ScanType %v, want %v", types[0].ScanType(), scan)
		}
	}
	var got []int64
	for outcome.Rows.Next() {
		var value any
		if err := outcome.Rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		v, ok := value.(int64)
		if !ok {
			t.Fatalf("raw INT4 payload %T, want int64", value)
		}
		got = append(got, v)
	}
	if err := outcome.Rows.Err(); err != nil {
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
	if err := outcome.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	if outcome.Stmt != nil {
		if err := outcome.Stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := outcome.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	if outcome.Stmt != nil {
		if err := outcome.Stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
func RowPIDs(t *testing.T, ctx context.Context, a, b *sql.Conn) (int, int) {
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
func LimitSequenceCapture(t *testing.T, parent context.Context, holder *sql.Tx) {
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
