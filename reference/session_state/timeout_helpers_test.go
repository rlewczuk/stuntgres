package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func timeoutReferenceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

type timeoutReferenceFixture struct {
	t                 *testing.T
	p                 *fixture.Pair
	b                 fixture.Backend
	owner, peer       *sql.Conn
	ownerPID, peerPID int
	blockedOID        int64
	ownerSessionID    string
	logSince          string
	logBaseline       string
}

func newTimeoutReferenceFixture(t *testing.T, p *fixture.Pair, b fixture.Backend) timeoutReferenceFixture {
	t.Helper()
	a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
	// Physical disposal, not borrower return, prevents armed timers or aborted
	// blocks leaking into later cases. Workers register later and join first.
	t.Cleanup(func() { _ = a.Raw(func(any) error { return driver.ErrBadConn }) })
	f := timeoutReferenceFixture{t: t, p: p, b: b, owner: a, peer: peer}
	f.exec(peer, "CREATE TABLE timeout_sentinel(v INT NOT NULL)", 0)
	f.exec(peer, "INSERT INTO timeout_sentinel VALUES(7)", 1)
	f.exec(peer, "CREATE TABLE timeout_blocked(v INT)", 0)
	f.captureOwner()
	if b.PG {
		f.peerPID = fixture.LockPID(t, p.Ctx, peer)
		f.blockedOID = fixture.LockOID(t, p.Ctx, "public.timeout_blocked")
	}
	return f
}

func (f timeoutReferenceFixture) exec(x fixture.Executor, q string, n int64) {
	f.t.Helper()
	fixture.RunStep(f.t, f.p.Ctx, x, f.b.Recorder, fixture.Exec(q, n))
}
func (f timeoutReferenceFixture) show(name, want string) {
	f.t.Helper()
	fixture.RunStep(f.t, f.p.Ctx, f.owner, f.b.Recorder, localReferenceSelect(f.b.PG, "SHOW "+name, name, want))
}
func (f timeoutReferenceFixture) rows(x fixture.Executor, values ...int64) {
	f.t.Helper()
	want := make([][]any, len(values))
	for i, v := range values {
		want[i] = []any{v}
	}
	fixture.RunStep(f.t, f.p.Ctx, x, f.b.Recorder, fixture.Select("SELECT v FROM timeout_sentinel ORDER BY v", []string{"v"}, []string{"INT4"}, want))
}
func (f timeoutReferenceFixture) set(name, value string) {
	f.t.Helper()
	f.exec(f.owner, "SET "+name+"='"+value+"'", 0)
}
func timeoutReferenceWant(code string) fixture.Diagnostic {
	severity, message := "FATAL", ""
	switch code {
	case "25P04":
		message = "terminating connection due to transaction timeout"
	case "25P03":
		message = "terminating connection due to idle-in-transaction timeout"
	case "57P05":
		message = "terminating connection due to idle-session timeout"
	case "57014":
		severity, message = "ERROR", "canceling statement due to statement timeout"
	case "55P03":
		severity, message = "ERROR", "canceling statement due to lock timeout"
	default:
		panic("unspecified timeout diagnostic")
	}
	return fixture.Diagnostic{Severity: severity, SeverityUnlocalized: severity, Code: code, Message: message}
}
func timeoutReferenceDiagnostic(t *testing.T, err error, code string) {
	t.Helper()
	want := timeoutReferenceWant(code)
	fixture.AssertDiagnostic(t, err, &want)
}

// No owner SQL during this bounded interval: this is the stimulus for idle
// expiry, not a synchronization substitute for proving a lock wait.
func timeoutReferenceIdle(t *testing.T, ctx context.Context, delay time.Duration) {
	t.Helper()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal("idle witness context expired", ctx.Err())
	}
}

// Every caller expects an expired owner, not a live-owner timing probe. Observe
// the asynchronous terminal transition before issuing the first fatal SQL.
func (f timeoutReferenceFixture) fatalProbe(code string) {
	f.t.Helper()
	f.awaitFatal(code)
	f.fatalQuery(code)
	f.dead()
}

// Elapsed timeout budget does not prove its asynchronous callback has run.
// Observe termination without owner SQL, preserving the first FATAL response for
// fatalProbe. PostgreSQL's exact log witness and native's public Validator report
// the actual physical owner's state; the fixture context bounds a broken timer.
func (f timeoutReferenceFixture) awaitFatal(code string) {
	f.t.Helper()
	if f.b.PG {
		f.serverFatal(code)
		return
	}
	for {
		var valid bool
		if err := f.owner.Raw(func(raw any) error {
			validator, ok := raw.(driver.Validator)
			if !ok {
				f.t.Fatal("timeout owner has no public Validator")
			}
			valid = validator.IsValid()
			return nil
		}); err != nil {
			f.t.Fatal("observing timeout owner", err)
		}
		if !valid {
			return
		}
		timeoutReferenceIdle(f.t, f.p.Ctx, 10*time.Millisecond)
	}
}

// A probe can restart the owner's idle timer before a delayed timeout callback
// runs. Retry complete idle windows, not rapid owner queries that prevent expiry.
// The deadline bounds broken timers; unexpected errors still fail immediately.
func (f timeoutReferenceFixture) awaitIdleFatal(code string, idle time.Duration) {
	f.t.Helper()
	f.awaitIdleFatalQuery(code, idle)
	f.dead()
}

// Leave retained consumers open until their post-fatal assertions run.
func (f timeoutReferenceFixture) awaitIdleFatalQuery(code string, idle time.Duration) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.p.Ctx, 5*time.Second)
	defer cancel()
	for delay := 2 * idle; ; delay = min(2*delay, time.Second) {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.t.Fatalf("waiting for idle timeout %s: %v", code, ctx.Err())
		case <-timer.C:
		}
		rows, err := f.owner.QueryContext(ctx, "SELECT v FROM timeout_sentinel")
		if rows != nil {
			if err != nil {
				rows.Close()
				f.t.Fatal("idle probe published rows with an error", err)
			}
			if err := rows.Close(); err != nil {
				f.t.Fatal("closing live idle probe", err)
			}
			continue
		}
		var state interface{ SQLState() string }
		if code == "25P03" && errors.As(err, &state) && state.SQLState() == "25P02" {
			want := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
			fixture.AssertDiagnostic(f.t, err, &want)
			continue
		}
		f.diagnostic(err, code)
		return
	}
}

// Transaction probes do not restart the BEGIN budget. Observe the terminal
// condition rather than assuming a timeout callback ran during a fixed delay.
// Keep this separate from idle probes, whose SQL would restart the idle budget.
// Do not dispose the owner here: retained Rows must be checked before dead().
func (f timeoutReferenceFixture) awaitTransactionFatal(ctx context.Context) {
	f.t.Helper()
	for {
		rows, err := f.owner.QueryContext(ctx, "SELECT 1")
		if err != nil {
			if rows != nil {
				rows.Close()
				f.t.Fatal("fatal transaction probe published rows", err)
			}
			if ctx.Err() != nil {
				f.t.Fatal("waiting for transaction timeout", ctx.Err())
			}
			f.diagnostic(err, "25P04")
			return
		}
		var value int
		if !rows.Next() {
			rows.Close()
			f.t.Fatal("live transaction probe omitted row", rows.Err())
		}
		if err := rows.Scan(&value); err != nil || value != 1 {
			rows.Close()
			f.t.Fatal("live transaction probe value", value, err)
		}
		if rows.Next() {
			rows.Close()
			f.t.Fatal("live transaction probe returned extra row")
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			f.t.Fatal("transaction probe published Rows before error", err)
		}
		if err := rows.Close(); err != nil {
			f.t.Fatal("closing live transaction probe", err)
		}
		timeoutReferenceIdle(f.t, ctx, 10*time.Millisecond)
	}
}

// Keep retained native Rows open through the first fatal, then close them
// before a later ErrBadConn probe asks database/sql to close the sql.Conn.
func (f timeoutReferenceFixture) fatalQuery(code string) {
	f.t.Helper()
	rows, err := f.owner.QueryContext(f.p.Ctx, "SELECT v FROM timeout_sentinel")
	if rows != nil {
		rows.Close()
		f.t.Fatal("fatal owner published rows")
	}
	// pq discards server FATAL as ErrBadConn; real server logs independently
	// establish the diagnostic, while native exports all portable fields.
	f.diagnostic(err, code)
}
func (f timeoutReferenceFixture) dead() {
	f.t.Helper()
	_, err := f.owner.ExecContext(f.p.Ctx, "INSERT INTO timeout_sentinel VALUES(99)")
	// database/sql pins an unusable owner after pq's terminal response; native
	// must likewise never admit another command on the terminated handle.
	timeoutReferenceDeadError(f.t, err)
}

func timeoutReferenceDeadError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, driver.ErrBadConn) && !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("terminated handle = %T %v, want ErrBadConn or ErrConnDone", err, err)
	}
}

const timeoutPrivateTransaction = "BEGIN; LOCK TABLE timeout_sentinel IN ACCESS EXCLUSIVE MODE; INSERT INTO timeout_sentinel VALUES(8)"

func (f timeoutReferenceFixture) beginPrivate() {
	f.t.Helper()
	f.exec(f.owner, timeoutPrivateTransaction, 1)
}
func (f timeoutReferenceFixture) holdBlocked() func() {
	f.t.Helper()
	f.exec(f.peer, "BEGIN", 0)
	f.exec(f.peer, "LOCK TABLE timeout_blocked IN ACCESS EXCLUSIVE MODE", 0)
	return func() { f.exec(f.peer, "ROLLBACK", 0) }
}
func (f timeoutReferenceFixture) blocked(phase, query string, held *sql.Stmt, executors ...fixture.Executor) fixture.RelationOutcome {
	f.t.Helper()
	var executor fixture.Executor = f.owner
	if len(executors) != 0 {
		executor = executors[0]
	}
	completed := make(chan struct{})
	await := fixture.LockOperation(f.t, f.p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(completed)
		switch phase {
		case "prepare":
			s, err := executor.PrepareContext(ctx, query)
			return fixture.RelationOutcome{Stmt: s, Err: err}
		case "held":
			r, err := held.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: r, Err: err}
		case "held-lock-share":
			r, err := held.ExecContext(ctx)
			return fixture.RelationOutcome{Result: r, Err: err}
		case "insert", "utility":
			r, err := executor.ExecContext(ctx, query)
			return fixture.RelationOutcome{Result: r, Err: err}
		default:
			r, err := executor.QueryContext(ctx, query)
			return fixture.RelationOutcome{Rows: r, Err: err}
		}
	})
	if f.b.PG {
		mode := "AccessShareLock"
		if phase == "insert" {
			mode = "RowExclusiveLock"
		}
		if phase == "utility" {
			mode = "AccessExclusiveLock"
		}
		// SQL owns these timers: completion can retire the request before an
		// observer sees it. Always join and validate that actual outcome below.
		fixture.LockWaitBehind(f.t, f.p.Ctx, f.ownerPID, f.peerPID, f.blockedOID, mode, completed)
	}
	o := await()
	if o.Result != nil {
		f.t.Fatal("interrupted work published result")
	}
	if o.Rows != nil {
		o.Rows.Close()
		f.t.Fatal("interrupted work published rows")
	}
	if o.Stmt != nil {
		o.Stmt.Close()
		f.t.Fatal("interrupted Prepare published handle")
	}
	return o
}

// Transaction lock publication can wake the peer before later TEMP cleanup.
// Observe that separate lifecycle resource under its own bounded deadline.
func (f timeoutReferenceFixture) tempAbsent() {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.p.Ctx, 2*time.Second)
	defer cancel()
	const q = "SELECT relname FROM pg_class WHERE relname='timeout_owner_temp'"
	for {
		rows, err := f.peer.QueryContext(ctx, q)
		if err != nil {
			f.t.Fatal("TEMP cleanup catalog witness", err)
		}
		found := rows.Next()
		if found {
			var name string
			if err := rows.Scan(&name); err != nil || name != "timeout_owner_temp" {
				rows.Close()
				f.t.Fatal("unexpected TEMP identity", name, err)
			}
			if rows.Next() {
				rows.Close()
				f.t.Fatal("duplicate TEMP identity")
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			f.t.Fatal("TEMP cleanup catalog rows", err)
		}
		if !found {
			break
		}
		timeoutReferenceIdle(f.t, ctx, 10*time.Millisecond)
	}
	fixture.RunStep(f.t, f.p.Ctx, f.peer, f.b.Recorder,
		fixture.Select(q, []string{"relname"}, []string{"NAME"}, nil))
}

// These clock brackets describe observations, not the server's unobservable
// fatal-publication instant. A timed-out preparation is never a passing witness.
func timeoutReferenceUntil(t *testing.T, ctx context.Context, target time.Time) {
	t.Helper()
	for remaining := time.Until(target); remaining > 0; remaining = time.Until(target) {
		timeoutReferenceIdle(t, ctx, min(remaining, 10*time.Millisecond))
	}
}

func (f timeoutReferenceFixture) preparationExec(q string, count int64) error {
	f.t.Helper()
	result, err := f.owner.ExecContext(f.p.Ctx, q)
	if err != nil {
		if result != nil {
			f.t.Fatal("failed timeout preparation published result", q, err)
		}
		return err
	}
	if result == nil {
		f.t.Fatal("successful timeout preparation omitted result", q)
	}
	n, err := result.RowsAffected()
	if err != nil || n != count {
		f.t.Fatal("timeout preparation affected count", q, n, err)
	}
	return nil
}

func (f timeoutReferenceFixture) preparationShow(want string) error {
	f.t.Helper()
	rows, err := f.owner.QueryContext(f.p.Ctx, "SHOW transaction_timeout")
	if err != nil {
		if rows != nil {
			rows.Close()
			f.t.Fatal("failed timeout preparation published rows", err)
		}
		return err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || len(names) != 1 || names[0] != "transaction_timeout" {
		f.t.Fatal("timeout preparation SHOW columns", names, err)
	}
	settingsTextMetadata(f.t, rows, f.b.PG, false)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			f.t.Fatal("timeout preparation published Rows before error", err)
		}
		f.t.Fatal("timeout preparation SHOW omitted row")
	}
	var got string
	if err := rows.Scan(&got); err != nil || got != want {
		f.t.Fatal("timeout preparation SHOW value", got, want, err)
	}
	if rows.Next() {
		f.t.Fatal("timeout preparation SHOW returned extra row")
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal("timeout preparation SHOW iteration", err)
	}
	if err := rows.Close(); err != nil {
		f.t.Fatal("timeout preparation SHOW close", err)
	}
	return nil
}

func (f timeoutReferenceFixture) preparationExpired(err error, earliest time.Time) {
	f.t.Helper()
	if time.Now().Before(earliest) {
		f.t.Fatal("timeout preparation failed before earliest configured expiry", err)
	}
	f.diagnostic(err, "25P04")
	f.dead()
}

func (f *timeoutReferenceFixture) replaceTimeoutOwner() {
	f.t.Helper()
	_ = f.owner.Raw(func(any) error { return driver.ErrBadConn })
	_ = f.owner.Close()
	owner, err := f.b.DB.Conn(f.p.Ctx)
	if err != nil {
		f.t.Fatal("replacing expired timeout preparation owner", err)
	}
	f.owner = owner
	f.t.Cleanup(func() {
		_ = owner.Raw(func(any) error { return driver.ErrBadConn })
		_ = owner.Close()
	})
	f.captureOwner()
}

// Return false only when the exact fatal was observed after BOTH possible
// deadlines. An armed wrong outcome is never an adaptation signal.
func (f timeoutReferenceFixture) originalExpiry(oldLatest, candidateEarliest, candidateLatest time.Time) bool {
	f.t.Helper()
	timeoutReferenceUntil(f.t, f.p.Ctx, oldLatest)
	f.awaitTransactionFatal(f.p.Ctx)
	observed := time.Now()
	f.dead()
	if observed.Before(candidateEarliest) {
		return true
	}
	if observed.Before(candidateLatest) {
		f.t.Fatal("timeout observation straddled competing deadline bracket")
	}
	return false
}

func (f timeoutReferenceFixture) requireAttemptTime(budget time.Duration) {
	f.t.Helper()
	deadline, ok := f.p.Ctx.Deadline()
	if !ok || time.Until(deadline) < 2*budget+2*time.Second {
		f.t.Fatal("no time remains for a decisive adaptive timeout witness", budget)
	}
}
