package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCopyProgressLifecycle(t *testing.T) {
	for _, phase := range []string{"open_transaction", "cancel", "initial_lock"} {
		t.Run(phase, func(t *testing.T) {
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				observer := vacuumConn(t, p, b)
				worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
				copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_lifetime(id INT PRIMARY KEY)")
				copyProgressExec(t, p.Ctx, observer, "INSERT INTO copy_progress_lifetime VALUES(99)")
				id := copyProgressIdentify(t, p, worker, "copy_progress_lifetime")
				if phase == "open_transaction" {
					copyProgressExec(t, p.Ctx, worker, "BEGIN")
					defer worker.ExecContext(p.Ctx, "ROLLBACK")
				}
				if phase == "initial_lock" {
					copyProgressExec(t, p.Ctx, observer, "BEGIN")
					defer observer.ExecContext(p.Ctx, "ROLLBACK")
					copyProgressExec(t, p.Ctx, observer, "LOCK TABLE copy_progress_lifetime IN ACCESS EXCLUSIVE MODE")
				}
				run := copyProgressStart(t, p, b, worker, "COPY copy_progress_lifetime FROM STDIN")
				if phase == "initial_lock" {
					if b.PG {
						holder := fixture.LockPID(t, p.Ctx, observer)
						fixture.RelationLockPending(t, p, b, int(id.pid), holder, id.relid, "RowExclusiveLock", run.finished)
					} else {
						fixture.NativeCopyRelationLockWait(t, p.Ctx, run.finished)
					}
					peer := vacuumConn(t, p, b)
					copyProgressIdle(t, p, peer, id.pid)
					copyProgressExec(t, p.Ctx, observer, "ROLLBACK")
				}
				s := copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
				copyProgressVisible(t, s, id, "COPY FROM")
				if phase == "cancel" {
					copyProgressCancelAdmitted(t, p, b, observer, id, run, "copy_progress_lifetime")
				} else {
					run.input.send(t, p, []byte("1\n"))
					run.finish(t, 1)
					if phase == "open_transaction" {
						copyProgressIdle(t, p, observer, id.pid)
						var own int64
						if err := worker.QueryRowContext(p.Ctx, "SELECT count(*) FROM copy_progress_lifetime").Scan(&own); err != nil || own != 2 {
							t.Fatal("transaction own rows", own, err)
						}
						copyProgressExec(t, p.Ctx, worker, "ROLLBACK")
					}
				}
				copyProgressIdle(t, p, observer, id.pid)
				wantCount := int64(2)
				if phase == "cancel" || phase == "open_transaction" {
					wantCount = 1
				}
				vacuumRun(t, p, b, observer, fixture.Select("SELECT count(*) AS n FROM copy_progress_lifetime", []string{"n"}, []string{"INT8"}, [][]any{{wantCount}}))
			})
		})
	}
	for _, phase := range []string{"reader_error", "writer_error", "short_write", "panic"} {
		t.Run(phase, func(t *testing.T) {
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				// Short writes are an explicit native host-API contract, like callback panic;
				// do not impose native io.ErrShortWrite behavior on pgconn's transport.
				if b.PG && phase == "short_write" {
					return
				}
				observer := vacuumConn(t, p, b)
				worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
				copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_io(id INT)")
				copyProgressExec(t, p.Ctx, observer, "INSERT INTO copy_progress_io VALUES(99)")
				id := copyProgressIdentify(t, p, worker, "copy_progress_io")
				failure := errors.New("COPY progress caller I/O failure")
				var result stuntgres.CopyResult
				var err error
				if phase == "reader_error" {
					run := copyProgressStart(t, p, b, worker, "COPY copy_progress_io FROM STDIN")
					copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
					run.input.send(t, p, []byte("1\n"))
					select {
					case run.input.chunks <- copyProgressChunk{err: failure}:
					case <-p.Ctx.Done():
						t.Fatal(p.Ctx.Err())
					}
					out := <-run.done
					run.joined = true
					result, err = out.result, out.err
				} else if phase == "panic" {
					// Arbitrary Go callback panic is a native-only host API boundary.
					if b.PG {
						return
					}
					marker := &struct{}{}
					gate := newCopyProgressInput()
					recovered := make(chan any, 1)
					joined := false
					ctx, cancel := context.WithCancel(p.Ctx)
					defer func() {
						cancel()
						gate.close()
						if !joined {
							<-recovered
						}
					}()
					go func() {
						defer func() { recovered <- recover() }()
						_, _ = fixture.CopyFrom(ctx, worker, false, "COPY copy_progress_io FROM STDIN", copyProgressPanicReader{gate, marker})
					}()
					copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
					gate.close()
					got := <-recovered
					joined = true
					if got != marker {
						t.Fatalf("panic identity=%v", got)
					}
					copyProgressIdle(t, p, observer, id.pid)
					vacuumRun(t, p, b, observer, fixture.Select("SELECT id FROM copy_progress_io", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
					return
				} else {
					result, err = fixture.CopyTo(p.Ctx, worker, b.PG, "COPY copy_progress_io TO STDOUT", copyProgressErrorWriter{failure, phase == "short_write"})
				}
				if err == nil || result != (stuntgres.CopyResult{}) {
					t.Fatalf("I/O failure returned success %+v err=%v", result, err)
				}
				if phase != "reader_error" && !errors.Is(err, failure) && !errors.Is(err, io.ErrShortWrite) {
					t.Fatalf("lost writer error identity %v", err)
				}
				if phase == "reader_error" && !b.PG && !errors.Is(err, failure) {
					t.Fatalf("lost native reader identity %v", err)
				}
				copyProgressIdle(t, p, observer, id.pid)
				vacuumRun(t, p, b, observer, fixture.Select("SELECT id FROM copy_progress_io", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
			})
		})
	}
}

type copyProgressPanicReader struct {
	gate   *copyProgressInput
	marker any
}

func (r copyProgressPanicReader) Read(dst []byte) (int, error) { r.gate.Read(dst); panic(r.marker) }

type copyProgressErrorWriter struct {
	err   error
	short bool
}

func (w copyProgressErrorWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, w.err
}

// Supply real tuple work before observing server input admission and signaling.
// Client reader prefetch alone cannot establish PostgreSQL's cancellation boundary.
func copyProgressCancelAdmitted(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, id copyProgressIdentity, run *copyProgressRun, table string) {
	t.Helper()
	run.input.send(t, p, []byte("1\n"))
	consumed := copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, present bool) bool { return present && s.bytes.Valid && s.bytes.Int64 > 0 })
	copyProgressVisible(t, consumed, id, "COPY FROM")
	if b.PG {
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			var reading bool
			if err := observer.QueryRowContext(p.Ctx, "SELECT COALESCE((SELECT state='active' AND wait_event='ClientRead' FROM pg_stat_activity WHERE pid=$1),false)", id.pid).Scan(&reading); err != nil {
				t.Fatal(err)
			}
			if reading {
				break
			}
			select {
			case <-tick.C:
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
		}
		var signaled bool
		if err := observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", id.pid).Scan(&signaled); err != nil || !signaled {
			t.Fatal("cancel signal", signaled, err)
		}
	} else {
		run.cancel()
	}
	run.input.close()
	out := <-run.done
	run.joined = true
	if out.err == nil || out.result != (stuntgres.CopyResult{}) {
		t.Fatalf("canceled COPY returned success %+v", out)
	}
	if b.PG {
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: "COPY " + table + ", line 2"}
		fixture.AssertDiagnostic(t, out.err, &want)
	} else if !errors.Is(out.err, context.Canceled) {
		t.Fatal(out.err)
	}
}
