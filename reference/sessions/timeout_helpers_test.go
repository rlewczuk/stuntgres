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

func (f timeoutReferenceFixture) fatalProbe(code string) {
	f.t.Helper()
	f.fatalQuery(code)
	f.dead()
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
func (f timeoutReferenceFixture) beginPrivate() {
	f.t.Helper()
	f.exec(f.owner, "BEGIN", 0)
	f.exec(f.owner, "LOCK TABLE timeout_sentinel IN ACCESS EXCLUSIVE MODE", 0)
	f.exec(f.owner, "INSERT INTO timeout_sentinel VALUES(8)", 1)
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
	await := fixture.LockOperation(f.t, f.p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
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
		fixture.LockWaitBehind(f.t, f.p.Ctx, f.ownerPID, f.peerPID, f.blockedOID, mode)
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
