package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type seriesCanceledResult struct {
	rows   *sql.Rows
	result sql.Result
	err    error
}

// Named worker plus real provider frames form the admission gate. A caller
// context Err probe cannot observe native advancement: operations derive their
// own cancel contexts, so observe the actual worker instead of assuming passthrough.
func seriesNativeCancelWorker(ctx context.Context, c *sql.Conn, st *sql.Stmt, q string, query bool, done chan<- seriesCanceledResult) {
	var r seriesCanceledResult
	if query {
		if st != nil {
			r.rows, r.err = st.QueryContext(ctx)
		} else {
			r.rows, r.err = c.QueryContext(ctx, q)
		}
	} else {
		if st != nil {
			r.result, r.err = st.ExecContext(ctx)
		} else {
			r.result, r.err = c.ExecContext(ctx, q)
		}
	}
	done <- r
}

func seriesNativeCancel(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, prepared, query bool, providerFrames ...string) {
	t.Helper()
	providerFrame := "functionSetCursor).next"
	if len(providerFrames) != 0 {
		providerFrame = providerFrames[0]
	}
	var st *sql.Stmt
	var err error
	if prepared {
		st, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	done := make(chan seriesCanceledResult, 1)
	go seriesNativeCancelWorker(ctx, c, st, q, query, done)
	joined := false
	defer func() {
		if !joined {
			cancel()
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case r := <-done:
				if r.rows != nil {
					r.rows.Close()
				}
			case <-timer.C:
				t.Error("native series worker failed to join")
			}
		}
	}()
	buffer := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buffer, true)
		// Check both frames in the SAME goroutine block, not independent workers.
		admitted := false
		for stack := range bytes.SplitSeq(buffer[:n], []byte("\n\n")) {
			if bytes.Contains(stack, []byte("seriesNativeCancelWorker")) && bytes.Contains(stack, []byte(providerFrame)) {
				admitted = true
				break
			}
		}
		if admitted {
			cancel()
			break
		}
		select {
		case r := <-done:
			joined = true
			if r.rows != nil {
				r.rows.Close()
			}
			t.Fatalf("series completed before native provider admission: %v", r.err)
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		default:
			runtime.Gosched()
		}
	}
	select {
	case r := <-done:
		joined = true
		if r.rows != nil {
			r.rows.Close()
			t.Fatal("published canceled series cursor")
		}
		if r.result != nil || !errors.Is(r.err, context.Canceled) {
			t.Fatal("native no-publication", r.result, r.err)
		}
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
}

// PostgreSQL admits the actual series command on the physical owner. A separate
// observer cancels that backend through the server and every worker is joined.
func seriesServerCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, prepared bool) {
	t.Helper()
	var pid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	observer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	var st *sql.Stmt
	if prepared {
		st, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
	}
	workerCtx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	type result struct {
		value sql.Result
		err   error
	}
	done := make(chan result, 1)
	go func() {
		var value sql.Result
		var err error
		if st != nil {
			value, err = st.ExecContext(workerCtx)
		} else {
			value, err = c.ExecContext(workerCtx, q)
		}
		done <- result{value, err}
	}()
	joined := false
	defer func() {
		if !joined {
			cancel()
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				t.Error("series worker failed to join")
			}
		}
	}()
	for {
		var active bool
		err = observer.QueryRowContext(p.Ctx, "SELECT state='active' AND query=$2 FROM pg_stat_activity WHERE pid=$1", pid, q).Scan(&active)
		if err != nil {
			t.Fatal(err)
		}
		if active {
			break
		}
		select {
		case r := <-done:
			joined = true
			t.Fatalf("series completed before server cancellation: %v", r.err)
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		default:
			runtime.Gosched()
		}
	}
	var canceled bool
	if err := observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
		t.Fatal(canceled, err)
	}
	select {
	case r := <-done:
		joined = true
		if r.value != nil {
			t.Fatal("published canceled result")
		}
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
		fixture.AssertDiagnostic(t, r.err, &want)
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, seriesRows(1)))
}

func TestReferenceSeriesLifecycle(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const huge = "SELECT generate_series(1::bigint,9223372036854775807::bigint) AS v"
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				if b.PG {
					seriesServerCancel(t, p, b, c, huge, prepared)
				} else {
					seriesNativeCancel(t, p, c, huge, prepared, true)
				}
			})
		}
		if b.PG {
			return
		} // Retained immutable/eager ownership is native, not pq streaming.
		ctx, cancel := context.WithCancel(p.Ctx)
		rows, err := c.QueryContext(ctx, "SELECT generate_series(1,3) AS v ORDER BY v")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		defer rows.Close()
		defer cancel()
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var saved int64
		if err := rows.Scan(&saved); err != nil {
			t.Fatal(err)
		}
		cancel()
		if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
			t.Fatal("retained cancellation", rows.Err())
		}
		rows.Close()
		rows.Close()
		if saved != 1 || len(cols) != 1 || cols[0].DatabaseTypeName() != "INT4" || cols[0].Name() != "v" {
			t.Fatal("saved data changed", saved, cols)
		}
		pre, cancelPre := context.WithCancel(p.Ctx)
		cancelPre()
		if r, err := c.QueryContext(pre, huge); r != nil || !errors.Is(err, context.Canceled) {
			if r != nil {
				r.Close()
			}
			t.Fatal(r, err)
		}
	})
}

func TestReferenceSeriesAtomicity(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE series_sink(v integer)", 0),
			fixture.Exec("INSERT INTO series_sink VALUES(99)", 1),
			fixture.Exec("CREATE TABLE series_input(id integer,step integer)", 0),
			fixture.Exec("INSERT INTO series_input VALUES(1,1),(2,0)", 2),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, q := range []string{
			"INSERT INTO series_sink SELECT generate_series(1,2,step) FROM (SELECT step FROM series_input ORDER BY id) s",
			"CREATE TABLE series_failed AS SELECT generate_series(1,2,step) AS v FROM (SELECT step FROM series_input ORDER BY id) s",
		} {
			t.Run(q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""))
					s.Prepared = prepared
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM series_sink", []string{"v"}, []string{"INT4"}, seriesRows(99)))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("SELECT * FROM series_failed", fixture.Diagnostic{Code: "42P01", Message: `relation "series_failed" does not exist`, Position: "15"}))
				}
			})
		}
		for _, q := range []string{
			"INSERT INTO series_sink SELECT generate_series(1,2147483647)",
			"CREATE TABLE series_canceled AS SELECT generate_series(1,2147483647) AS v",
		} {
			t.Run("cancel/"+q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					if b.PG {
						seriesServerCancel(t, p, b, c, q, prepared)
					} else {
						seriesNativeCancel(t, p, c, q, prepared, false)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM series_sink", []string{"v"}, []string{"INT4"}, seriesRows(99)))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("SELECT * FROM series_canceled", fixture.Diagnostic{Code: "42P01", Message: `relation "series_canceled" does not exist`, Position: "15"}))
				}
			})
		}
	})
}
