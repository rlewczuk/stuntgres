package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

func timeoutReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) { body(t, p, b) })
	}
}

type timeoutReferenceFixture struct {
	t                 *testing.T
	p                 *referencePair
	b                 referenceBackend
	owner, peer       *sql.Conn
	ownerPID, peerPID int
	blockedOID        int64
	logBaseline       string
	logSince          string
}

func newTimeoutReferenceFixture(t *testing.T, p *referencePair, b referenceBackend) timeoutReferenceFixture {
	t.Helper()
	a, peer := referenceLockConnections(t, p.ctx, b.db)
	// Physical disposal, not borrower return, prevents armed timers or aborted
	// blocks leaking into later cases. Workers register later and join first.
	t.Cleanup(func() { _ = a.Raw(func(any) error { return driver.ErrBadConn }) })
	f := timeoutReferenceFixture{t: t, p: p, b: b, owner: a, peer: peer}
	f.exec(peer, "CREATE TABLE timeout_sentinel(v INT NOT NULL)", 0)
	f.exec(peer, "INSERT INTO timeout_sentinel VALUES(7)", 1)
	f.exec(peer, "CREATE TABLE timeout_blocked(v INT)", 0)
	f.captureOwner()
	if b.pg {
		f.peerPID = referenceLockPID(t, p.ctx, peer)
		f.blockedOID = referenceLockOID(t, p.ctx, "public.timeout_blocked")
	}
	return f
}

func (f timeoutReferenceFixture) exec(x referenceExecutor, q string, n int64) {
	f.t.Helper()
	referenceRunStep(f.t, f.p.ctx, x, f.b.recorder, referenceExec(q, n))
}
func (f timeoutReferenceFixture) show(name, want string) {
	f.t.Helper()
	referenceRunStep(f.t, f.p.ctx, f.owner, f.b.recorder, localReferenceSelect(f.b.pg, "SHOW "+name, name, want))
}
func (f timeoutReferenceFixture) rows(x referenceExecutor, values ...int64) {
	f.t.Helper()
	want := make([][]any, len(values))
	for i, v := range values {
		want[i] = []any{v}
	}
	referenceRunStep(f.t, f.p.ctx, x, f.b.recorder, referenceSelect("SELECT v FROM timeout_sentinel ORDER BY v", []string{"v"}, []string{"INT4"}, want))
}
func (f timeoutReferenceFixture) set(name, value string) {
	f.t.Helper()
	f.exec(f.owner, "SET "+name+"='"+value+"'", 0)
}
func timeoutReferenceWant(code string) referenceDiagnostic {
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
	return referenceDiagnostic{Severity: severity, SeverityUnlocalized: severity, Code: code, Message: message}
}
func timeoutReferenceDiagnostic(t *testing.T, err error, code string) {
	t.Helper()
	want := timeoutReferenceWant(code)
	referenceAssertDiagnostic(t, err, &want)
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
	rows, err := f.owner.QueryContext(f.p.ctx, "SELECT v FROM timeout_sentinel")
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
	_, err := f.owner.ExecContext(f.p.ctx, "INSERT INTO timeout_sentinel VALUES(99)")
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
func (f timeoutReferenceFixture) blocked(phase, query string, held *sql.Stmt, executors ...referenceExecutor) relationOutcome {
	f.t.Helper()
	var executor referenceExecutor = f.owner
	if len(executors) != 0 {
		executor = executors[0]
	}
	await := referenceLockOperation(f.t, f.p.ctx, func(ctx context.Context) relationOutcome {
		switch phase {
		case "prepare":
			s, err := executor.PrepareContext(ctx, query)
			return relationOutcome{stmt: s, err: err}
		case "held":
			r, err := held.QueryContext(ctx)
			return relationOutcome{rows: r, err: err}
		case "held-lock-share":
			r, err := held.ExecContext(ctx)
			return relationOutcome{result: r, err: err}
		case "insert", "utility":
			r, err := executor.ExecContext(ctx, query)
			return relationOutcome{result: r, err: err}
		default:
			r, err := executor.QueryContext(ctx, query)
			return relationOutcome{rows: r, err: err}
		}
	})
	if f.b.pg {
		mode := "AccessShareLock"
		if phase == "insert" {
			mode = "RowExclusiveLock"
		}
		if phase == "utility" {
			mode = "AccessExclusiveLock"
		}
		referenceLockWaitBehind(f.t, f.p.ctx, f.ownerPID, f.peerPID, f.blockedOID, mode)
	}
	o := await()
	if o.result != nil {
		f.t.Fatal("interrupted work published result")
	}
	if o.rows != nil {
		o.rows.Close()
		f.t.Fatal("interrupted work published rows")
	}
	if o.stmt != nil {
		o.stmt.Close()
		f.t.Fatal("interrupted Prepare published handle")
	}
	return o
}

// Transaction lock publication can wake the peer before later TEMP cleanup.
// Observe that separate lifecycle resource under its own bounded deadline.
func (f timeoutReferenceFixture) tempAbsent() {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.p.ctx, 2*time.Second)
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
	referenceRunStep(f.t, f.p.ctx, f.peer, f.b.recorder,
		referenceSelect(q, []string{"relname"}, []string{"NAME"}, nil))
}
