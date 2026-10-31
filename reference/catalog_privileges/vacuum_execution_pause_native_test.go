package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func vacuumPauseNative(t *testing.T, p *fixture.Pair, b fixture.Backend, worker, observer *sql.Conn, table string, executor fixture.Executor, query string) {
	t.Helper()
	// No operation was launched: canceled/deadline ownership stays ordinary.
	for _, deadline := range []bool{false, true} {
		pause := stuntgres.NewExecutionPause("vacuum.inspect")
		t.Cleanup(pause.Resume)
		ctx, cancel := context.WithCancel(p.Ctx)
		want := context.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(p.Ctx, time.Unix(1, 0))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		_, err := executor.ExecContext(stuntgres.WithExecutionPause(ctx, pause), query)
		cancel()
		fixture.LexicalOrdinary(t, err, want)
		vacuumPauseUnadmitted(t, pause)
		vacuumPauseHealthy(t, p, b, worker, observer)
	}

	// Absent, wrong point, explicit nil and early Resume cannot hold work.
	count := int64(1)
	for _, mode := range []string{"absent", "wrong", "nil", "early"} {
		pause := stuntgres.NewExecutionPause("vacuum.inspect")
		t.Cleanup(pause.Resume)
		ctx := p.Ctx
		switch mode {
		case "wrong":
			pause = stuntgres.NewExecutionPause("vacuum.not-a-point")
			t.Cleanup(pause.Resume)
			ctx = stuntgres.WithExecutionPause(ctx, pause)
		case "nil":
			ctx = stuntgres.WithExecutionPause(stuntgres.WithExecutionPause(ctx, pause), nil)
		case "early":
			pause.Resume()
			pause.Resume()
			ctx = stuntgres.WithExecutionPause(ctx, pause)
		}
		out := vacuumPauseLaunch(t, ctx, executor, query, pause).join()
		vacuumPauseSuccess(t, out)
		if mode == "early" {
			if err := pause.Wait(p.Ctx); err == nil || err.Error() != "stuntgres: execution pause resumed before admission" {
				t.Fatal("early Resume lost ordinary early-release error", err)
			}
		} else {
			vacuumPauseUnadmitted(t, pause)
		}
		count++
		vacuumCount(t, p.Ctx, worker, table, count)
		vacuumPauseHealthy(t, p, b, worker, observer)
	}

	// Inner replacement alone admits. A canceled observer neither cancels nor
	// releases the operation; a later independent Wait still observes admission.
	outer, inner := stuntgres.NewExecutionPause("vacuum.inspect"), stuntgres.NewExecutionPause("vacuum.inspect")
	ctx := stuntgres.WithExecutionPause(stuntgres.WithExecutionPause(p.Ctx, outer), inner)
	r := vacuumPauseLaunch(t, ctx, executor, query, outer, inner)
	r.admitted(t, p.Ctx, inner)
	vacuumPauseUnadmitted(t, outer)
	wait, stop := context.WithCancel(p.Ctx)
	stop()
	// Wait cancellation is tested before admission below as well; after
	// admission the documented ready admission may win over canceled Wait.
	if err := inner.Wait(p.Ctx); err != nil {
		t.Fatal("replacement lost admission", err)
	}
	inner.Resume()
	vacuumPauseSuccess(t, r.join())
	count++
	vacuumCount(t, p.Ctx, worker, table, count)
	vacuumPauseHealthy(t, p, b, worker, observer)

	pause := stuntgres.NewExecutionPause("vacuum.inspect")
	fixture.LexicalOrdinary(t, pause.Wait(wait), context.Canceled)
	cause := errors.New("vacuum request owner canceled")
	source, cancelCause := context.WithCancelCause(p.Ctx)
	t.Cleanup(func() { cancelCause(nil) })
	scoped := stuntgres.WithExecutionPause(source, pause)
	gotDeadline, gotOK := scoped.Deadline()
	wantDeadline, wantOK := source.Deadline()
	if gotDeadline != wantDeadline || gotOK != wantOK || scoped.Done() != source.Done() {
		t.Fatal("pause changed source deadline/cancellation")
	}
	r = vacuumPauseLaunch(t, scoped, executor, query, pause)
	r.admitted(t, p.Ctx, pause)
	// The previously canceled Wait did not release work; this worker remains
	// held while an independent request and independent observer execute SQL.
	unrelated := stuntgres.NewExecutionPause("vacuum.inspect")
	t.Cleanup(unrelated.Resume)
	vacuumPauseUnadmitted(t, unrelated)
	vacuumPauseUnadmitted(t, outer)
	for range 3 {
		vacuumRun(t, p, b, observer, fixture.Select("SELECT 7 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
	if err := pause.Wait(p.Ctx); err != nil {
		t.Fatal("canceled Wait released held work", err)
	}
	select {
	case <-r.finished:
		t.Fatal("canceled Wait completed work")
	default:
	}
	cancelCause(cause)
	vacuumPauseCanceled(t, r.join())
	if context.Cause(source) != cause {
		t.Fatal("lost source cancellation cause", context.Cause(source))
	}
	vacuumCount(t, p.Ctx, worker, table, count)
	vacuumPauseHealthy(t, p, b, worker, observer)
	vacuumPauseData(t, p, b, worker, table)

	vacuumPauseSignals(t, p, b, worker, observer, table, executor, query, count)
	vacuumPauseCycles(t, p, b, worker, observer, table+"_cycles")
	vacuumPauseIndependent(t, p, b, observer, table+"_independent")
}

func vacuumPauseSignals(t *testing.T, p *fixture.Pair, b fixture.Backend, worker, observer *sql.Conn, table string, executor fixture.Executor, query string, count int64) {
	t.Helper()
	var pid int
	if err := worker.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	source, cancel := context.WithCancel(p.Ctx)
	t.Cleanup(cancel)
	pause := stuntgres.NewExecutionPause("vacuum.inspect")
	r := vacuumPauseLaunch(t, stuntgres.WithExecutionPause(source, pause), executor, query, pause)
	r.admitted(t, p.Ctx, pause)
	vacuumRun(t, p, b, observer, fixture.Select("SELECT pg_cancel_backend($1) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, pid))
	out := r.join()
	if out.Result != nil || out.Rows != nil {
		t.Fatal("SQL canceled VACUUM published output", out)
	}
	want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
	fixture.AssertDiagnostic(t, out.Err, &want)
	if source.Err() != nil || context.Cause(source) != nil {
		t.Fatal("SQL cancellation canceled source", source.Err())
	}
	vacuumCount(t, p.Ctx, worker, table, count)
	vacuumPauseHealthy(t, p, b, worker, observer)

	// Fatal cancellation belongs to a disposable physical owner, not a source
	// context substitution or a reusable borrower from the healthy worker.
	victim, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal("disposable physical owner", err)
	}
	t.Cleanup(func() {
		// The deliberate post-FATAL ErrBadConn probe below makes database/sql
		// retire this borrower itself. ErrConnDone is then its exact Close state.
		if err := victim.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Error("closing terminated physical owner", err)
		}
	})
	pid = fixture.LockPID(t, p.Ctx, victim)
	pause = stuntgres.NewExecutionPause("vacuum.inspect")
	r = vacuumPauseLaunch(t, stuntgres.WithExecutionPause(source, pause), victim, query, pause)
	r.admitted(t, p.Ctx, pause)
	vacuumRun(t, p, b, observer, fixture.Select("SELECT pg_terminate_backend($1,1000) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, pid))
	out = r.join()
	if out.Result != nil || out.Rows != nil {
		t.Fatal("terminated VACUUM published output", out)
	}
	fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"})
	if source.Err() != nil {
		t.Fatal("termination canceled source", source.Err())
	}
	// The active operation above owns the sole FATAL report. Subsequent use
	// must return the pre-admission sentinel, not another SQL diagnostic or
	// successful work; database/sql then retires the pinned borrower.
	_, err = victim.ExecContext(p.Ctx, "SELECT 1")
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatal("post-FATAL physical owner reuse", err)
	}
	_, err = victim.ExecContext(p.Ctx, "SELECT 1")
	if !errors.Is(err, sql.ErrConnDone) {
		t.Fatal("retired borrower remained open", err)
	}
	vacuumCount(t, p.Ctx, worker, table, count)
	vacuumPauseHealthy(t, p, b, worker, observer)
	vacuumPauseData(t, p, b, worker, table)
}

func vacuumPauseCycles(t *testing.T, p *fixture.Pair, b fixture.Backend, worker, observer *sql.Conn, table string) {
	t.Helper()
	vacuumPauseSeed(t, p, b, worker, table)
	query := "VACUUM (PROCESS_TOAST false) " + table
	var stale []*stuntgres.ExecutionPause
	for cycle, want := range []int64{1, 1, 2} {
		pause := stuntgres.NewExecutionPause("vacuum.inspect")
		r := vacuumPauseLaunch(t, stuntgres.WithExecutionPause(p.Ctx, pause), worker, query, pause)
		r.admitted(t, p.Ctx, pause)
		for _, old := range stale {
			old.Resume()
		}
		select {
		case <-r.finished:
			t.Fatal("stale hold released fresh request")
		default:
		}
		if cycle == 1 {
			r.cancel()
			vacuumPauseCanceled(t, r.join())
		} else {
			pause.Resume()
			vacuumPauseSuccess(t, r.join())
		}
		stale = append(stale, pause)
		vacuumCount(t, p.Ctx, worker, table, want)
		vacuumPauseData(t, p, b, worker, table)
		vacuumPauseHealthy(t, p, b, worker, observer)
	}
}

func vacuumPauseIndependent(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, table string) {
	t.Helper()
	a, bconn := vacuumConn(t, p, b), vacuumConn(t, p, b)
	vacuumPauseSeed(t, p, b, a, table+"_a")
	vacuumPauseSeed(t, p, b, bconn, table+"_b")
	bid := copyProgressIdentify(t, p, bconn, table+"_b")
	pa, pb := stuntgres.NewExecutionPause("vacuum.inspect"), stuntgres.NewExecutionPause("vacuum.inspect")
	// B's context is attached before A executes, but only B can admit it.
	bctx := stuntgres.WithExecutionPause(p.Ctx, pb)
	ra := vacuumPauseLaunch(t, stuntgres.WithExecutionPause(p.Ctx, pa), a, "VACUUM "+table+"_a", pa)
	ra.admitted(t, p.Ctx, pa)
	vacuumPauseUnadmitted(t, pb)
	rb := vacuumPauseLaunch(t, bctx, bconn, "VACUUM "+table+"_b", pb)
	rb.admitted(t, p.Ctx, pb)
	brow, present := vacuumProgressScan(t, observer.QueryRowContext(p.Ctx, vacuumProgressQuery, bid.pid))
	if !present {
		t.Fatal("independent B missing")
	}
	vacuumProgressVisible(t, brow, bid, false)
	pa.Resume()
	vacuumPauseSuccess(t, ra.join())
	fresh, present := vacuumProgressScan(t, observer.QueryRowContext(p.Ctx, vacuumProgressQuery, bid.pid))
	if !present {
		t.Fatal("releasing A released B")
	}
	vacuumProgressVisible(t, fresh, bid, false)
	select {
	case <-rb.finished:
		t.Fatal("independent B completed before its cancellation")
	default:
	}
	rb.cancel()
	vacuumPauseCanceled(t, rb.join())
	vacuumCount(t, p.Ctx, a, table+"_a", 1)
	vacuumCount(t, p.Ctx, bconn, table+"_b", 0)
	vacuumPauseData(t, p, b, a, table+"_a")
	vacuumPauseData(t, p, b, bconn, table+"_b")
	vacuumPauseHealthy(t, p, b, a, observer)
	vacuumPauseHealthy(t, p, b, bconn, observer)
}
