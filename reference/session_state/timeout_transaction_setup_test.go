package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"stuntgres/reference/fixture"
	"time"
)

// Assignment, AbortRestore and completion fixtures may replace pre-decisive owners.
// The child allowance never borrows the four seconds reserved for semantics.
func (f *timeoutReferenceFixture) transactionSetupContext() (context.Context, context.CancelFunc) {
	f.t.Helper()
	end, ok := f.p.Ctx.Deadline()
	if !ok || time.Until(end) <= 4*time.Second {
		f.t.Fatal("transaction setup lacks four-second semantic reserve")
	}
	end = end.Add(-4 * time.Second)
	if limit := time.Now().Add(6 * time.Second); limit.Before(end) {
		end = limit
	}
	f.t.Cleanup(func() {
		_ = f.owner.Raw(func(any) error { return driver.ErrBadConn })
		_ = f.owner.Close()
	})
	return context.WithDeadline(f.p.Ctx, end)
}

func (f timeoutReferenceFixture) transactionSetupExec(ctx context.Context, query string, count int64) error {
	f.t.Helper()
	result, err := f.owner.ExecContext(ctx, query)
	if err != nil {
		if result != nil {
			f.t.Fatal("failed transaction setup published result", query, err)
		}
		return err
	}
	if result == nil {
		f.t.Fatal("transaction setup omitted result", query)
	}
	if n, err := result.RowsAffected(); err != nil || n != count {
		f.t.Fatal("transaction setup affected count", query, n, err)
	}
	if _, err := result.LastInsertId(); err == nil {
		f.t.Fatal("transaction setup LastInsertId unexpectedly supported", query)
	}
	return nil
}

func (f timeoutReferenceFixture) transactionSetupText(ctx context.Context, query, column, want string, nullable bool) error {
	f.t.Helper()
	recorder := f.b.Recorder
	recorder.Mu.Lock()
	firstOperation := len(recorder.Operations)
	recorder.Mu.Unlock()
	ctx = context.WithValue(ctx, fixture.RecorderKey{}, recorder)
	rows, err := f.owner.QueryContext(ctx, query)
	if err != nil {
		if rows != nil {
			rows.Close()
			f.t.Fatal("failed transaction setup published rows", query, err)
		}
		return err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || len(names) != 1 || names[0] != column {
		f.t.Fatal("transaction setup columns", query, names, err)
	}
	settingsTextMetadata(f.t, rows, f.b.PG, nullable)
	if !rows.Next() {
		f.t.Fatal("transaction setup omitted row", query, rows.Err())
	}
	var got any
	if err := rows.Scan(&got); err != nil {
		f.t.Fatal("transaction setup scan", query, err)
	}
	fixture.AssertRawValue(f.t, f.b.PG, "TEXT", got)
	if fixture.Normalize(got) != want {
		f.t.Fatal("transaction setup value", query, got, want)
	}
	if rows.Next() {
		f.t.Fatal("transaction setup returned extra row", query)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal("transaction setup published Rows before error", query, err)
	}
	if err := rows.Close(); err != nil {
		f.t.Fatal("transaction setup close", query, err)
	}
	// Inspect this query's actual consumed stream, never an earlier matching SHOW.
	recorder.Mu.Lock()
	var recorded *fixture.Operation
	for i := len(recorder.Operations) - 1; i >= firstOperation; i-- {
		op := recorder.Operations[i]
		if op.SQL == query && op.Err == nil && op.Columns != nil {
			recorded = op
			break
		}
	}
	if recorded == nil {
		recorder.Mu.Unlock()
		f.t.Fatal("successful transaction setup query has no recorded result", query)
	}
	recordedRows := recorded.Rows // Recorder cells are immutable after capture.
	columns := slices.Clone(recorded.Columns)
	closed, closeErr := recorded.Closed, recorded.CloseErr
	recorder.Mu.Unlock()
	captured := make([][]any, 0, len(recordedRows))
	for _, row := range recordedRows {
		values := make([]any, len(row))
		for i, value := range row {
			fixture.AssertRawValue(f.t, f.b.PG, "TEXT", value)
			values[i] = fixture.Normalize(value)
		}
		captured = append(captured, values)
	}
	step := fixture.Step{Columns: []string{column}, Rows: [][]any{{want}}}
	if !slices.Equal(columns, step.Columns) || !fixture.RecordedRowsMatch(captured, step) {
		f.t.Fatal("transaction setup recorded query data", query, columns, captured)
	}
	if !closed || closeErr != nil {
		f.t.Fatal("transaction setup recorded cursor close", query, closed, closeErr)
	}
	return nil
}

func (f timeoutReferenceFixture) transactionSetupExpiry(ctx context.Context, err error, observed, earliest time.Time) {
	f.t.Helper()
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		f.t.Fatal("transaction setup context failure, not configured expiry", ctx.Err(), err)
	}
	if observed.Before(earliest) {
		f.t.Fatal("transaction setup returned before earliest configured expiry", err)
	}
	f.preparationExpired(err, earliest)
}

func (f *timeoutReferenceFixture) prepareReenable(kind string, mark func(string)) any {
	f.t.Helper()
	ctx, cancel := f.transactionSetupContext()
	defer cancel()
	for attempt := 1; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			f.t.Fatal("reenable setup allowance expired", ctx.Err())
		}
		identity := fixture.SettingsPhysical(f.t, f.owner)
		var beginBefore, beginAfter, rearmBefore time.Time
		expired := false
		queries := []string{"SET transaction_timeout='250ms'", "BEGIN", "SET transaction_timeout='0'", "SHOW transaction_timeout", "SET transaction_timeout='250ms'", "SHOW transaction_timeout"}
		if kind == "set-config" {
			queries[2] = "SELECT set_config('transaction_timeout','0',false)"
		}
		for i, q := range queries {
			if i == 3 {
				timeoutReferenceIdle(f.t, ctx, 350*time.Millisecond)
			}
			if i == 5 {
				timeoutReferenceIdle(f.t, ctx, 50*time.Millisecond)
			}
			mark(q)
			before := time.Now()
			var err error
			switch {
			case i == 2 && kind == "set-config":
				err = f.transactionSetupText(ctx, q, "set_config", "0", true)
			case i == 3:
				err = f.transactionSetupText(ctx, q, "transaction_timeout", "0", false)
			case i == 5:
				err = f.transactionSetupText(ctx, q, "transaction_timeout", "250ms", false)
			default:
				err = f.transactionSetupExec(ctx, q, 0)
			}
			after := time.Now()
			if i == 1 {
				beginBefore, beginAfter = before, after
			}
			if i == 4 {
				rearmBefore = before
			}
			earliest := beginBefore.Add(250 * time.Millisecond)
			if i == 0 {
				earliest = before.Add(250 * time.Millisecond)
			} else if i >= 4 {
				earliest = rearmBefore.Add(250 * time.Millisecond)
			}
			if err != nil {
				f.t.Logf("%s setup attempt=%d query=%s command=[%v,%v] BEGIN=[%v,%v] earliest=%v transaction=250ms error=%v", kind, attempt, q, before, after, beginBefore, beginAfter, earliest, err)
				if i == 3 {
					f.t.Fatal("successfully disabled timer did not survive350ms", err)
				}
				f.transactionSetupExpiry(ctx, err, after, earliest)
				f.rows(f.peer, 7)
				expired = true
				break
			}
			if ctx.Err() != nil {
				f.t.Fatal("reenable setup allowance expired", ctx.Err())
			}
			if fixture.SettingsPhysical(f.t, f.owner) != identity {
				f.t.Fatal("reenable setup changed physical owner", q)
			}
		}
		if !expired {
			return identity // Successful SHOW250 ends adaptation irreversibly.
		}
		_ = f.owner.Raw(func(any) error { return driver.ErrBadConn })
		_ = f.owner.Close()
		if attempt == 3 {
			f.t.Fatal("reenable setup exhausted three fixed250ms attempts")
		}
		f.replaceTimeoutOwner()
	}
	panic("unreachable reenable setup")
}

func (f *timeoutReferenceFixture) prepareAbortRestore(failure string, child bool) (any, func()) {
	f.t.Helper()
	ctx, cancel := f.transactionSetupContext()
	defer cancel()
	for attempt := 1; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			f.t.Fatal("abort setup allowance expired", ctx.Err())
		}
		identity := fixture.SettingsPhysical(f.t, f.owner)
		var beginBefore, beginAfter time.Time
		expired := false
		release := func() {}
		queries := []string{"SET transaction_timeout='500ms'", "SET idle_in_transaction_session_timeout='1s'", "BEGIN", "LOCK TABLE timeout_sentinel IN ACCESS EXCLUSIVE MODE", "INSERT INTO timeout_sentinel VALUES(8)"}
		if child {
			queries = append(queries, "SAVEPOINT timeout_child")
		}
		for i, q := range queries {
			before := time.Now()
			count := int64(0)
			if i == 4 {
				count = 1
			}
			err := f.transactionSetupExec(ctx, q, count)
			after := time.Now()
			if i == 2 {
				beginBefore, beginAfter = before, after
			}
			earliest := beginBefore.Add(500 * time.Millisecond)
			if i < 2 {
				earliest = before.Add(500 * time.Millisecond)
			}
			if err != nil {
				f.t.Logf("abort setup attempt=%d query=%s command=[%v,%v] BEGIN=[%v,%v] earliest=%v transaction=500ms idle=1s short=0 error=%v", attempt, q, before, after, beginBefore, beginAfter, earliest, err)
				f.transactionSetupExpiry(ctx, err, after, earliest)
				expired = true
				break
			}
			if ctx.Err() != nil {
				f.t.Fatal("abort setup allowance expired", ctx.Err())
			}
			if fixture.SettingsPhysical(f.t, f.owner) != identity {
				f.t.Fatal("abort setup changed physical owner", q)
			}
		}
		if !expired && failure != "semantic" {
			fixture.RunStep(f.t, ctx, f.peer, f.b.Recorder, fixture.Exec("BEGIN", 0))
			held := true
			release = func() {
				if held {
					f.exec(f.peer, "ROLLBACK", 0)
					held = false
				}
			}
			f.t.Cleanup(release)
			fixture.RunStep(f.t, ctx, f.peer, f.b.Recorder, fixture.Exec("LOCK TABLE timeout_blocked IN ACCESS EXCLUSIVE MODE", 0))
		}
		if !expired && !time.Now().Before(beginBefore.Add(500*time.Millisecond)) {
			f.t.Logf("abort setup attempt=%d preoperation elapsed gate BEGIN=[%v,%v] observed=%v transaction=500ms idle=1s short=0", attempt, beginBefore, beginAfter, time.Now())
			f.awaitTransactionFatal(ctx)
			f.dead()
			expired = true
		}
		if !expired && failure != "semantic" {
			q := "SET " + failure + "_timeout='125ms'"
			before := time.Now()
			err := f.transactionSetupExec(ctx, q, 0)
			after := time.Now()
			if err != nil {
				f.t.Logf("abort setup attempt=%d query=%s command=[%v,%v] BEGIN=[%v,%v] transaction=500ms short=125ms error=%v", attempt, q, before, after, beginBefore, beginAfter, err)
				f.transactionSetupExpiry(ctx, err, after, beginBefore.Add(500*time.Millisecond))
				expired = true
			}
		}
		if ctx.Err() != nil {
			f.t.Fatal("abort setup allowance expired before operation", ctx.Err())
		}
		if !expired {
			return identity, release // Actual semantic INSERT / blocked LOCK is next, never retried.
		}
		release()
		f.rows(f.peer, 7) // Proves private8 rollback and sentinel AX release.
		_ = f.owner.Raw(func(any) error { return driver.ErrBadConn })
		_ = f.owner.Close()
		if attempt == 3 {
			f.t.Fatal("abort setup exhausted three fixed500ms attempts")
		}
		f.replaceTimeoutOwner()
	}
	panic("unreachable abort setup")
}
