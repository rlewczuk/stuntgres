package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwCursorNative(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	if b.PG {
		return
	}
	batch := func(q string, values [][]any) fixture.Step {
		return fixture.Select(q, []string{"id", "label"}, []string{"INT4", "TEXT"}, values)
	}
	consume := func(h fdwHeldResult, values [][]any) {
		fixture.RunStep(t, p.Ctx, h, h.recorder, batch(h.query, values))
	}
	endFailure := errors.New("real SQL reader terminal failure")
	endHook := func(ctx context.Context, method string) error {
		if method == "EndForeignScan" {
			if ctx.Err() != nil {
				return errors.New("cleanup context is canceled")
			}
			var count int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_foreign_table").Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return errors.New("terminal observer lost foreign metadata")
			}
			return endFailure
		}
		return ctx.Err()
	}
	execFailure := func(query string, want error) {
		t.Helper()
		_, err := c.ExecContext(p.Ctx, query)
		if !errors.Is(err, want) {
			t.Fatalf("%s got %v want %v", query, err, want)
		}
	}
	defer r.setHook(nil)
	if state := r.snapshot(); state.begins != state.ends || state.openPools != 0 || state.openCursors != 0 || state.openRows != 0 {
		t.Fatal("portable cursor phases leaked resources", state)
	}

	before := r.snapshot()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE fdw_finite NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
	if after := r.snapshot(); after != before {
		t.Fatal("DECLARE opened or drained the reader", before, after)
	}
	first := fdwHeld(t, p, b, c, "FETCH 1 FROM fdw_finite")
	frontier := r.snapshot()
	if frontier.begins-before.begins != 1 || frontier.ends != before.ends || frontier.iterations-before.iterations != 1 || frontier.generations-before.generations != 1 || frontier.openCursors != 1 || frontier.openPools != 1 || frontier.openRows != 0 {
		t.Fatal("finite FETCH did not retain exactly its actual frontier", before, frontier)
	}
	consume(first, [][]any{{int64(1), "one"}})
	if after := r.snapshot(); after != frontier {
		t.Fatal("public batch consumption advanced the reader", frontier, after)
	}
	second := fdwHeld(t, p, b, c, "FETCH 1 FROM fdw_finite")
	continued := r.snapshot()
	if continued.begins != frontier.begins || continued.generations != frontier.generations || continued.iterations != frontier.iterations+1 || continued.ends != frontier.ends {
		t.Fatal("later FETCH reopened/skipped/ended its source", frontier, continued)
	}
	r.setHook(endHook)
	execFailure("CLOSE fdw_finite", endFailure)
	fdwReleased(t, r, before, 1)
	r.setHook(nil)
	run(fixture.Exec("ROLLBACK", 0))
	consume(second, [][]any{{int64(2), nil}})

	// All detached scans must finish, even when the first End fails.
	before = r.snapshot()
	run(fixture.Exec("BEGIN", 0))
	for _, name := range []string{"fdw_close_a", "fdw_close_b"} {
		run(fixture.Exec("DECLARE "+name+" NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
		run(batch("FETCH 1 FROM "+name, [][]any{{int64(1), "one"}}))
	}
	r.setHook(endHook)
	execFailure("CLOSE ALL", endFailure)
	fdwReleased(t, r, before, 2)
	r.setHook(nil)
	run(fixture.Exec("ROLLBACK", 0))

	// An EOF-only End error vetoes the newly requested eager batch.
	before = r.snapshot()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE fdw_end_eof NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
	run(batch("FETCH 1 FROM fdw_end_eof", [][]any{{int64(1), "one"}}))
	r.setHook(endHook)
	fdwHostQueryError(t, p.Ctx, c, "FETCH ALL FROM fdw_end_eof", endFailure)
	fdwReleased(t, r, before, 1)
	r.setHook(nil)
	run(fixture.Exec("ROLLBACK", 0))

	// Source-read failure occurs after a genuinely published first batch.
	before = r.snapshot()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE fdw_read_bad NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
	retained := fdwHeld(t, p, b, c, "FETCH 1 FROM fdw_read_bad")
	readFailure := errors.New("real later source read failed")
	r.setHook(func(ctx context.Context, method string) error {
		if method == "IterateForeignScan" {
			return readFailure
		}
		if method == "EndForeignScan" {
			return endFailure
		}
		return ctx.Err()
	})
	fdwHostQueryError(t, p.Ctx, c, "FETCH 1 FROM fdw_read_bad", readFailure)
	fdwReleased(t, r, before, 1)
	r.setHook(nil)
	run(fixture.Exec("ROLLBACK", 0))
	consume(retained, [][]any{{int64(1), "one"}})

	// Savepoint cleanup reaches a peer through the real End callback. This
	// would deadlock if registry rollback still called End under its old locks.
	before = r.snapshot()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("SAVEPOINT fdw_dispose_child", 0))
	run(fixture.Exec("DECLARE fdw_dispose NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
	run(batch("FETCH 1 FROM fdw_dispose", [][]any{{int64(1), "one"}}))
	r.setHook(func(ctx context.Context, method string) error {
		if method == "EndForeignScan" {
			var count int64
			return b.DB.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_foreign_table").Scan(&count)
		}
		return ctx.Err()
	})
	run(fixture.Exec("ROLLBACK TO fdw_dispose_child", 0))
	fdwReleased(t, r, before, 1)
	r.setHook(nil)
	run(fixture.Exec("ROLLBACK", 0))

	run(fixture.Exec("CREATE TABLE fdw_commit_marker(v integer)", 0))
	for _, hold := range []bool{false, true} {
		before = r.snapshot()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO fdw_commit_marker VALUES(1)", 1))
		declaration := "DECLARE fdw_commit NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data"
		if hold {
			declaration = "DECLARE fdw_commit NO SCROLL CURSOR WITH HOLD FOR SELECT id,label FROM fdw_data"
		}
		run(fixture.Exec(declaration, 0))
		run(batch("FETCH 1 FROM fdw_commit", [][]any{{int64(1), "one"}}))
		r.setHook(endHook)
		execFailure("COMMIT", endFailure)
		fdwReleased(t, r, before, 1)
		r.setHook(nil)
		run(fixture.Select("SELECT count(*) FROM fdw_commit_marker", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
	for _, terminal := range []string{"ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
		before = r.snapshot()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO fdw_commit_marker VALUES(1)", 1))
		run(fixture.Exec("DECLARE fdw_tx_end NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
		run(batch("FETCH 1 FROM fdw_tx_end", [][]any{{int64(1), "one"}}))
		r.setHook(endHook)
		execFailure(terminal, endFailure)
		fdwReleased(t, r, before, 1)
		r.setHook(nil)
		run(fixture.Select("SELECT count(*) FROM fdw_commit_marker", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
	before = r.snapshot()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO fdw_commit_marker VALUES(1)", 1))
	run(fixture.Exec("DECLARE fdw_hold_bad NO SCROLL CURSOR WITH HOLD FOR SELECT id,label FROM fdw_data", 0))
	drainedFirst := fdwHeld(t, p, b, c, "FETCH 1 FROM fdw_hold_bad")
	drainFailure := errors.New("real HOLD remainder read failed")
	r.setHook(func(ctx context.Context, method string) error {
		if method == "IterateForeignScan" {
			return drainFailure
		}
		if method == "EndForeignScan" {
			return endFailure
		}
		return ctx.Err()
	})
	execFailure("COMMIT", drainFailure)
	fdwReleased(t, r, before, 1)
	r.setHook(nil)
	run(fixture.Select("SELECT count(*) FROM fdw_commit_marker", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	consume(drainedFirst, [][]any{{int64(1), "one"}})

	before = r.snapshot()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE fdw_promoted NO SCROLL CURSOR WITH HOLD FOR SELECT id,label FROM fdw_data", 0))
	promoted := fdwHeld(t, p, b, c, "FETCH 1 FROM fdw_promoted")
	run(fixture.Exec("COMMIT", 0))
	finished := fdwReleased(t, r, before, 1)
	run(batch("FETCH ALL FROM fdw_promoted", [][]any{{int64(2), nil}, {int64(3), "three"}}))
	consume(promoted, [][]any{{int64(1), "one"}})
	run(fixture.Exec("CLOSE fdw_promoted", 0))
	if after := r.snapshot(); after != finished {
		t.Fatal("promoted cursor performed provider I/O", finished, after)
	}
	fdwCursorCancellation(t, p, b, c, r, run)
	// database/sql transaction terminal methods share the same fallible
	// prepublication cleanup, rather than hiding it after native writes commit.
	for _, commit := range []bool{false, true} {
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		before = r.snapshot()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO fdw_commit_marker VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DECLARE fdw_driver_tx NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, batch("FETCH 1 FROM fdw_driver_tx", [][]any{{int64(1), "one"}}))
		r.setHook(endHook)
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if !errors.Is(err, endFailure) {
			t.Fatal("driver transaction hid terminal cleanup failure", commit, err)
		}
		fdwReleased(t, r, before, 1)
		r.setHook(nil)
		run(fixture.Select("SELECT count(*) FROM fdw_commit_marker", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}

	// The unwrapped companion exposes the actual public driver.Conn.Close
	// contract, including its EndSession error (pool return cannot report it).
	owner, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	before = r.snapshot()
	for _, q := range []string{"BEGIN", "DECLARE fdw_session NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data"} {
		fixture.RunStep(t, p.Ctx, owner, &fixture.Recorder{RawBackend: fixture.RawNative}, fixture.Exec(q, 0))
	}
	sessionRows, err := owner.QueryContext(p.Ctx, "FETCH 1 FROM fdw_session")
	if err != nil {
		t.Fatal(err)
	}
	defer sessionRows.Close()
	fixture.DriverBasicMetadata(t, sessionRows, false, []string{"INT4", "TEXT"}, []bool{true, true})
	fixture.DeleteReferenceConsume(t, false, sessionRows, nil, batch("FETCH 1 FROM fdw_session", [][]any{{int64(1), "one"}}))
	r.setHook(endHook)
	err = owner.Raw(func(raw any) error { return raw.(driver.Conn).Close() })
	if !errors.Is(err, endFailure) {
		t.Fatal("session cleanup error was discarded", err)
	}
	fdwReleased(t, r, before, 1)
	r.setHook(nil)
}

type fdwCursorContextKey struct{}

func fdwCursorCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	before := r.snapshot()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE fdw_cancel NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
	first := fdwHeld(t, p, b, c, "FETCH 1 FROM fdw_cancel")
	ctx, cancel := context.WithCancel(context.WithValue(p.Ctx, fdwCursorContextKey{}, "second-request"))
	entered, done := make(chan struct{}), make(chan error, 1)
	cleanupBad := false
	r.setHook(func(ctx context.Context, method string) error {
		if method == "IterateForeignScan" {
			if ctx.Value(fdwCursorContextKey{}) != "second-request" {
				return errors.New("cursor retained an old request context")
			}
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		if method == "EndForeignScan" {
			cleanupBad = ctx.Err() != nil || ctx.Value(fdwCursorContextKey{}) != "second-request"
			return errors.New("secondary canceled-cursor cleanup failure")
		}
		return ctx.Err()
	})
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
		r.setHook(nil)
	}()
	go func() {
		rows, err := c.QueryContext(ctx, "FETCH 1 FROM fdw_cancel")
		if rows != nil {
			rows.Close()
			if err == nil {
				err = errors.New("canceled FETCH published rows")
			}
		}
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		joined = true
		t.Fatalf("cursor did not enter current-request read: %v", err)
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	cancel()
	err := <-done
	joined = true
	if !errors.Is(err, context.Canceled) || cleanupBad {
		t.Fatal("cancellation/cleanup context", err, cleanupBad)
	}
	finished := fdwReleased(t, r, before, 1)
	r.setHook(nil)
	run(fixture.Exec("ROLLBACK", 0))
	fixture.RunStep(t, p.Ctx, first, first.recorder, fixture.Select(first.query, []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
	if after := r.snapshot(); after != finished {
		t.Fatal("retained canceled-cursor batch performed I/O", finished, after)
	}
}
