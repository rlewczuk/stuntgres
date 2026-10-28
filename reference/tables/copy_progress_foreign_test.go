package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCopyProgressForeign(t *testing.T) {
	for _, phase := range []string{"terminal_error", "terminal_cancel", "terminal_panic"} {
		t.Run(phase, func(t *testing.T) { foreignProgressTerminalFailure(t, phase) })
	}
	for _, size := range []int{1, 2} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("batch%d/failure%v", size, fail), func(t *testing.T) {
				fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE copy_progress_foreign_control(id INT)", 0))
					workerPool := fixture.CopyScenarioPool(t, p, b.PG, "", "")
					worker, err := workerPool.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer worker.Close()
					foreignProgressHeld(t, p, b, observer, worker, "COPY copy_progress_foreign_control FROM STDIN", strings.Repeat("1\n", 2000), 2000)
					// Arbitrary native Go provider suppression/failure has no PG equivalent.
					if b.PG {
						return
					}
					fdwLive(t, p, b, reader, run)
					reader.writer.batchSize = size
					reader.writer.rewrite = func(row stuntgres.ForeignRow) (stuntgres.ForeignRow, bool) { return row, row[0] != int64(5) }
					foreignProgressProviderHeld(t, p, observer, worker, reader.writer, size, fail)
					if size == 2 && !reflect.DeepEqual(reader.writer.batches, []int{2, 2}) {
						t.Fatalf("progress changed provider batching: %v", reader.writer.batches)
					}
					if size == 1 && len(reader.writer.batches) != 0 {
						t.Fatalf("single-row provider unexpectedly batched: %v", reader.writer.batches)
					}
					expected := [][]any{{int64(4)}, {int64(6)}, {int64(7)}}
					if fail {
						expected = nil
					}
					run(fixture.Select("SELECT id FROM fdw_data WHERE id>3 ORDER BY id", []string{"id"}, []string{"INT4"}, expected))
				})
			})
		}
	}
}

func foreignProgressProviderHeld(t *testing.T, p *fixture.Pair, observer, worker *sql.Conn, w *fdwSQLWriter, size int, fail bool) {
	t.Helper()
	var pid int64
	if err := worker.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	terminalEntered, terminalRelease := make(chan struct{}), make(chan struct{})
	var terminalOnce sync.Once
	unblockTerminal := func() { terminalOnce.Do(func() { close(terminalRelease) }) }
	failure := errors.New("COPY progress provider failure")
	calls := 0
	boundary := "ExecForeignInsert"
	if size > 1 {
		boundary = "ExecForeignBatchInsert"
	}
	w.writeHook = func(ctx context.Context, name string) error {
		if name == boundary {
			calls++
			if calls == 2 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				if fail {
					return failure
				}
			}
		}
		if name == "EndForeignInsert" {
			close(terminalEntered)
			<-terminalRelease
		}
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	type outcome struct {
		result stuntgres.CopyResult
		err    error
	}
	done := make(chan outcome, 1)
	joined := false
	defer func() {
		cancel()
		unblock()
		unblockTerminal()
		if !joined {
			<-done
		}
		w.writeHook = nil
	}()
	// EOF/FinishInput precedes all mutation. Gate the second real provider unit,
	// after the first returned result was accepted, not the input reader.
	go func() {
		result, err := fixture.CopyFrom(ctx, worker, false, "COPY fdw_data FROM STDIN", strings.NewReader("4\tfour\tt\n5\tfive\tt\n6\tsix\tt\n7\tseven\tt\n"))
		done <- outcome{result, err}
	}()
	select {
	case <-entered:
	case out := <-done:
		joined = true
		t.Fatalf("provider ended before work gate: %+v", out)
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	var processed, excluded, skipped, total int64
	if err := observer.QueryRowContext(p.Ctx, "SELECT tuples_processed,tuples_excluded,tuples_skipped,bytes_total FROM pg_stat_progress_copy WHERE pid=$1", pid).Scan(&processed, &excluded, &skipped, &total); err != nil {
		t.Fatal(err)
	}
	if processed != 1 || excluded != 0 || skipped != 0 || total != 0 {
		t.Fatalf("provider accepted-work counters: %d %d %d %d", processed, excluded, skipped, total)
	}
	unblock()
	// PostgreSQL ends COPY progress only after its terminal foreign insert hook.
	// Native Finish owns this hook; the same registration must still be active.
	select {
	case <-terminalEntered:
	case out := <-done:
		joined = true
		t.Fatalf("COPY ended before terminal provider gate: %+v", out)
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	if err := observer.QueryRowContext(p.Ctx, "SELECT tuples_processed,tuples_excluded,tuples_skipped,bytes_total FROM pg_stat_progress_copy WHERE pid=$1", pid).Scan(&processed, &excluded, &skipped, &total); err != nil {
		t.Fatal("progress ended before terminal provider callback", err)
	}
	expected := int64(3)
	if fail {
		expected = 1
	}
	if processed != expected || excluded != 0 || skipped != 0 || total != 0 {
		t.Fatalf("terminal provider counters got=%d/%d/%d/%d expected processed=%d", processed, excluded, skipped, total, expected)
	}
	unblockTerminal()
	out := <-done
	joined = true
	if fail {
		if !errors.Is(out.err, failure) || out.result != (stuntgres.CopyResult{}) {
			t.Fatalf("provider failure: %+v", out)
		}
	} else if out.err != nil || out.result.Count != 3 || out.result.CommandTag != "COPY 3" {
		t.Fatalf("provider result: %+v", out)
	}
	var retained int64
	if err := observer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_progress_copy WHERE pid=$1", pid).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("retained provider row=%d err=%v", retained, err)
	}
}

func foreignProgressHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, observer, worker *sql.Conn, query, payload string, count int64) {
	t.Helper()
	var pid int64
	if err := worker.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	input, writer := io.Pipe()
	defer input.Close()
	type outcome struct {
		result stuntgres.CopyResult
		err    error
	}
	done := make(chan outcome, 1)
	joined := false
	defer func() {
		cancel()
		writer.Close()
		if !joined {
			<-done
		}
	}()
	go func() { r, e := fixture.CopyFrom(ctx, worker, b.PG, query, input); done <- outcome{r, e} }()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	wait := func(expected int64) {
		for {
			var processed, total, excluded, skipped sql.NullInt64
			err := observer.QueryRowContext(p.Ctx, "SELECT tuples_processed,bytes_total,tuples_excluded,tuples_skipped FROM pg_stat_progress_copy WHERE pid=$1", pid).Scan(&processed, &total, &excluded, &skipped)
			if err != nil && err != sql.ErrNoRows {
				t.Fatal(err)
			}
			if err == nil && processed.Valid && processed.Int64 == expected {
				if !total.Valid || total.Int64 != 0 || !excluded.Valid || excluded.Int64 != 0 || !skipped.Valid || skipped.Int64 != 0 {
					t.Fatalf("provider counters total=%v excluded=%v skipped=%v", total, excluded, skipped)
				}
				return
			}
			select {
			case <-tick.C:
			case <-p.Ctx.Done():
				t.Fatal("provider progress", p.Ctx.Err())
			}
		}
	}
	wait(0)
	if _, err := io.WriteString(writer, payload); err != nil {
		t.Fatal(err)
	}
	wait(count)
	writer.Close()
	out := <-done
	joined = true
	if out.err != nil || out.result.Count != count {
		t.Fatalf("provider result=%+v err=%v", out.result, out.err)
	}
	var retained int64
	if err := observer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_progress_copy WHERE pid=$1", pid).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("retained foreign progress=%d err=%v", retained, err)
	}
}
