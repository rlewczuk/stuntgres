package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func foreignProgressTerminalFailure(t *testing.T, phase string) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		// Arbitrary Go callback errors/panics and detached native callback contexts
		// have no PostgreSQL counterpart. The shared queued AFTER SQL is separate.
		if b.PG {
			return
		}
		fdwLive(t, p, b, reader, run)
		pool := fixture.CopyScenarioPool(t, p, false, "", "")
		worker, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer worker.Close()
		pid := fixture.LockPID(t, p.Ctx, worker)
		w := reader.writer
		begins, ends := w.copyBegins, w.copyEnds
		statementEnds, transactions := w.statementEnds, w.transactions
		commits, rollbacks := w.commits, w.rollbacks
		failure := errors.New("terminal EndForeignInsert failure")
		cleanupFailure := errors.New("secondary terminal statement cleanup failure")
		marker := &struct{ phase string }{phase}
		entered := make(chan context.Context, 1)
		release := make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		w.writeHook = func(ctx context.Context, name string) error {
			if name == "EndForeignInsert" {
				entered <- ctx
				<-release
				switch phase {
				case "terminal_error":
					return failure
				case "terminal_panic":
					panic(marker)
				}
			}
			if phase == "terminal_panic" && name == "EndStatement" {
				return cleanupFailure
			}
			return ctx.Err()
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		type outcome struct {
			result stuntgres.CopyResult
			err    error
			panic  any
		}
		done := make(chan outcome, 1)
		joined := false
		defer func() {
			cancel()
			unblock()
			if !joined {
				<-done
			}
			w.writeHook = nil
		}()
		go func() {
			var out outcome
			defer func() {
				out.panic = recover()
				done <- out
			}()
			out.result, out.err = fixture.CopyFrom(ctx, worker, false, "COPY fdw_data FROM STDIN", strings.NewReader("4\tfour\tt\n5\tfive\tf\n"))
		}()
		var terminalContext context.Context
		select {
		case terminalContext = <-entered:
		case out := <-done:
			joined = true
			t.Fatalf("COPY finished before actual terminal hook: %+v", out)
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		active := func() {
			t.Helper()
			var processed, excluded, skipped, total int64
			if err := observer.QueryRowContext(p.Ctx, "SELECT tuples_processed,tuples_excluded,tuples_skipped,bytes_total FROM pg_stat_progress_copy WHERE pid=$1", pid).Scan(&processed, &excluded, &skipped, &total); err != nil {
				t.Fatal("progress absent during actual terminal hook", err)
			}
			if processed != 2 || excluded != 0 || skipped != 0 || total != 0 {
				t.Fatalf("terminal counters %d/%d/%d/%d", processed, excluded, skipped, total)
			}
		}
		active()
		if terminalContext.Done() != nil || terminalContext.Err() != nil {
			t.Fatal("terminal hook did not receive detached cleanup context")
		}
		if phase == "terminal_cancel" {
			cancel()
			// Cancellation cannot release this detached terminal callback. It is
			// genuinely still held, and progress must remain until manual release.
			select {
			case out := <-done:
				joined = true
				t.Fatalf("COPY escaped blocked detached terminal hook: %+v", out)
			default:
			}
			active()
		}
		unblock()
		var out outcome
		select {
		case out = <-done:
			joined = true
		case <-p.Ctx.Done():
			t.Fatal("terminal callback cleanup did not join", p.Ctx.Err())
		}
		if out.result != (stuntgres.CopyResult{}) {
			t.Fatalf("failed terminal COPY returned success: %+v", out)
		}
		switch phase {
		case "terminal_error":
			if out.panic != nil || !errors.Is(out.err, failure) {
				t.Fatalf("terminal error identity: %+v", out)
			}
		case "terminal_cancel":
			if out.panic != nil || !errors.Is(out.err, context.Canceled) {
				t.Fatalf("terminal cancellation identity: %+v", out)
			}
		case "terminal_panic":
			if out.panic != marker || out.err != nil {
				t.Fatalf("terminal primary panic identity: %+v", out)
			}
		}
		w.writeHook = nil
		if w.copyBegins != begins+1 || w.copyEnds != ends+1 || w.statementEnds != statementEnds+1 || w.transactions != transactions+1 || w.commits != commits || w.rollbacks != rollbacks+1 {
			t.Fatalf("terminal cleanup/replay: begin=%d end=%d statements=%d transactions=%d commits=%d rollbacks=%d; baseline %d/%d/%d/%d/%d/%d", w.copyBegins, w.copyEnds, w.statementEnds, w.transactions, w.commits, w.rollbacks, begins, ends, statementEnds, transactions, commits, rollbacks)
		}
		var retained int64
		if err := observer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_progress_copy WHERE pid=$1", pid).Scan(&retained); err != nil || retained != 0 {
			t.Fatalf("terminal progress retained=%d err=%v", retained, err)
		}
		// The public Raw panic contract preserves this exact physical connection.
		// A bounded real command proves COPY reservation/admission were released;
		// the original PID and empty source effects prove reuse, not retirement.
		reuse, reuseCancel := context.WithTimeout(p.Ctx, 5*time.Second)
		defer reuseCancel()
		var actualPID, rows int64
		if err := worker.QueryRowContext(reuse, "SELECT pg_backend_pid(),count(*) FROM fdw_data WHERE id>3").Scan(&actualPID, &rows); err != nil || actualPID != int64(pid) || rows != 0 {
			t.Fatalf("same physical connection/rollback: pid=%d rows=%d err=%v", actualPID, rows, err)
		}
		run(fixture.Select("SELECT id FROM fdw_data WHERE id>3 ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
	})
}
