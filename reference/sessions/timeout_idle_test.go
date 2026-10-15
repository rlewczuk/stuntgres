package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimeoutIdleBoundaries(t *testing.T) {
	for _, kind := range []string{"outside", "inside", "aborted", "wrong-outside", "wrong-inside", "query-reset", "prepare-reset", "ping-reset", "retained-rows", "active-outside", "active-inside"} {
		t.Run(kind, func(t *testing.T) {
			timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				if kind == "query-reset" || kind == "prepare-reset" || kind == "ping-reset" {
					timeoutReferenceAdaptiveIdleReset(t, p, b, kind)
					return
				}
				f := newTimeoutReferenceFixture(t, p, b)
				identity := settingsPhysical(t, f.owner)
				switch kind {
				case "outside", "inside", "aborted":
					setting, code := "idle_session_timeout", "57P05"
					if kind != "outside" {
						setting, code = "idle_in_transaction_session_timeout", "25P03"
					}
					f.set(setting, "250ms")
					if kind != "outside" {
						f.beginPrivate()
					}
					if kind == "aborted" {
						_, err := f.owner.ExecContext(p.Ctx, "SELECT timeout_missing")
						want := fixture.ControlsReferenceDiagnostic("42703", `column "timeout_missing" does not exist`)
						want.Position = "8"
						fixture.AssertDiagnostic(t, err, &want)
					}
					f.awaitIdleFatal(code, 250*time.Millisecond)
					f.rows(f.peer, 7)
				case "wrong-outside", "wrong-inside":
					setting := "idle_in_transaction_session_timeout"
					if kind == "wrong-inside" {
						setting = "idle_session_timeout"
					}
					f.set(setting, "250ms")
					if kind == "wrong-inside" {
						f.beginPrivate()
					}
					timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
					f.show(setting, "250ms")
					if kind == "wrong-inside" {
						f.exec(f.owner, "ROLLBACK", 0)
					}
					if settingsPhysical(t, f.owner) != identity {
						t.Fatal("wrong idle category killed owner")
					}
					f.rows(f.peer, 7)
				case "retained-rows":
					f.set("idle_session_timeout", "250ms")
					rows, err := f.owner.QueryContext(p.Ctx, "SELECT v FROM timeout_sentinel")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					check := settingsTimeoutCursor(t, rows, b.PG)
					timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
					f.fatalQuery("57P05")
					if !b.PG {
						check()
					}
					f.dead()
					f.rows(f.peer, 7)
				case "active-outside", "active-inside":
					held, err := f.owner.PrepareContext(p.Ctx, "SELECT v FROM timeout_blocked")
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { held.Close() })
					release := f.holdBlocked()
					f.set("idle_session_timeout", "100ms")
					f.set("idle_in_transaction_session_timeout", "100ms")
					f.set("lock_timeout", "250ms")
					if kind == "active-inside" {
						f.exec(f.owner, "BEGIN", 0)
					}
					// Actual wait under an unreleased holder lasts 2.5 idle limits.
					// If active work were idle, a FATAL would replace this ERROR.
					o := f.blocked("held", "SELECT v FROM timeout_blocked", held)
					f.diagnostic(o.Err, "55P03", "held")
					if kind == "active-inside" {
						f.exec(f.owner, "ROLLBACK", 0)
					}
					f.set("idle_session_timeout", "0")
					f.set("idle_in_transaction_session_timeout", "0")
					release()
					if settingsPhysical(t, f.owner) != identity {
						t.Fatal("active lock wait killed owner")
					}
					f.rows(f.peer, 7)
				}
			})
		})
	}
}

func TestReferenceTimeoutPrecedence(t *testing.T) {
	// These are the new transaction suppression cases. The established
	// TestReferenceRelationLockWaitsTimeout owns statement-versus-lock ties.
	for _, tc := range []struct{ name, statement, idle, lock, code string }{
		{"statement-equal", "250ms", "0", "0", "25P04"},
		{"statement-longer", "1s", "0", "0", "25P04"},
		{"statement-shorter", "125ms", "0", "0", "57014"},
		{"idle-equal", "0", "250ms", "0", "25P04"},
		{"idle-longer", "0", "1s", "0", "25P04"},
		{"idle-shorter", "0", "125ms", "0", "25P03"},
		{"lock-shorter", "0", "0", "125ms", "55P03"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				f := newTimeoutReferenceFixture(t, p, b)
				f.set("statement_timeout", tc.statement)
				f.set("idle_in_transaction_session_timeout", tc.idle)
				f.set("lock_timeout", tc.lock)
				f.set("transaction_timeout", "250ms")
				f.beginPrivate()
				if tc.idle != "0" {
					timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
					f.fatalProbe(tc.code)
				} else {
					release := f.holdBlocked()
					o := f.blocked("utility", "LOCK TABLE timeout_blocked IN ACCESS EXCLUSIVE MODE", nil)
					f.diagnostic(o.Err, tc.code)
					if tc.code == "25P04" {
						f.dead()
					} else {
						f.exec(f.owner, "ROLLBACK", 0)
					}
					release()
				}
				f.rows(f.peer, 7)
			})
		})
	}
	t.Run("old-transaction-residual", func(t *testing.T) {
		timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			f := newTimeoutReferenceFixture(t, p, b)
			f.set("transaction_timeout", "500ms")
			f.beginPrivate()
			timeoutReferenceIdle(t, p.Ctx, 300*time.Millisecond)
			f.set("idle_in_transaction_session_timeout", "375ms")
			// Nominally shorter idle budget loses to the old transaction's
			// remaining 200ms, not to a restarted transaction timer.
			timeoutReferenceIdle(t, p.Ctx, 275*time.Millisecond)
			f.fatalProbe("25P04")
			f.rows(f.peer, 7)
		})
	})
	t.Run("admitted-set-config", timeoutReferencePrecedenceAssignment)
	t.Run("old-transaction-beats-shorter-statement", func(t *testing.T) {
		timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			f := newTimeoutReferenceFixture(t, p, b)
			release := f.holdBlocked()
			f.set("transaction_timeout", "500ms")
			f.beginPrivate()
			timeoutReferenceIdle(t, p.Ctx, 300*time.Millisecond)
			f.set("statement_timeout", "375ms")
			o := f.blocked("utility", "LOCK TABLE timeout_blocked IN ACCESS EXCLUSIVE MODE", nil)
			f.diagnostic(o.Err, "25P04")
			f.dead()
			release()
			f.rows(f.peer, 7)
		})
	})
}

// Kept with the precedence contract rather than a new SQL capability:
// supported set_config evaluates inside the admitted statement, and a later
// real wait independently witnesses the changed suppression policy.
func timeoutReferencePrecedenceAssignment(t *testing.T) {
	t.Helper()
	timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		f := newTimeoutReferenceFixture(t, p, b)
		f.set("statement_timeout", "250ms")
		f.set("transaction_timeout", "250ms")
		f.exec(f.owner, "BEGIN", 0)
		fixture.RunStep(t, p.Ctx, f.owner, b.Recorder,
			localReferenceSelect(b.PG, "SELECT set_config('transaction_timeout','0',false)", "set_config", "0"))
		timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
		f.show("transaction_timeout", "0")
		release := f.holdBlocked()
		o := f.blocked("utility", "LOCK TABLE timeout_blocked IN ACCESS EXCLUSIVE MODE", nil)
		f.diagnostic(o.Err, "57014")
		f.exec(f.owner, "ROLLBACK", 0)
		release()
		f.rows(f.peer, 7)
	})
}

func timeoutReferenceAdaptiveIdleReset(t *testing.T, p *fixture.Pair, b fixture.Backend, kind string) {
	t.Helper()
	const (
		initialIdle = 250 * time.Millisecond
		idleGrowth  = 2
		maximumIdle = 2 * time.Second
	)
	f := newTimeoutReferenceFixture(t, p, b)
	dispose := func(owner *sql.Conn) {
		// Returning an armed borrower could retain its timeout in the pool.
		// Dispose the physical connection, even if its timer has already fired.
		_ = owner.Raw(func(any) error { return driver.ErrBadConn })
		_ = owner.Close()
	}
	defer func() { dispose(f.owner) }()
	for idle := initialIdle; idle <= maximumIdle; idle *= idleGrowth {
		// Three half-budget waits, eventual expiry, and bounded SQL/log probes
		// must fit the existing scenario context; never extend its30s deadline.
		deadline, ok := p.Ctx.Deadline()
		if !ok || time.Until(deadline) < 5*idle/2+3*time.Second {
			t.Fatalf("%s: insufficient scenario time for idle budget %s", kind, idle)
		}
		identity := settingsPhysical(t, f.owner)
		gap := timeoutReferenceIdleResetAttempt(t, f, kind, idle, identity)
		if gap == 0 {
			return
		}
		if idle == maximumIdle {
			t.Fatalf("%s: exhausted idle budgets %s..%s (growth %d); pre-operation gap %s left insufficient headroom", kind, initialIdle, maximumIdle, idleGrowth, gap)
		}
		t.Logf("%s: measured pre-operation gap %s exhausted headroom for %s; replacing owner before trying %s", kind, gap, idle, idle*idleGrowth)
		dispose(f.owner)
		owner, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal("acquiring fresh idle-reset owner", err)
		}
		f.owner = owner
		if settingsPhysical(t, owner) == identity {
			t.Fatal("adaptive idle retry reused the disposed physical owner")
		}
		f.captureOwner()
	}
}

// A nonzero return rejects only a measured scheduling-invalid interval, before
// issuing its next operation. Every issued SQL/Prepare/Ping/identity error fails
// immediately; no returned error, including a timeout, is an adaptation signal.
func timeoutReferenceIdleResetAttempt(t *testing.T, f timeoutReferenceFixture, kind string, idle time.Duration, identity any) time.Duration {
	t.Helper()
	lastStart := time.Now()
	f.set("idle_session_timeout", idle.String())
	armed := time.Now()
	for range 3 {
		timeoutReferenceIdle(t, f.p.Ctx, idle/2)
		// The actual ready boundary cannot precede the prior operation's start.
		// This conservative upper bound also covers its response/assertion work.
		// Reserve a quarter-budget for dispatch rather than racing the deadline.
		if gap := time.Since(lastStart); gap >= 3*idle/4 {
			return gap
		}
		lastStart = time.Now()
		switch kind {
		case "query-reset":
			f.show("idle_session_timeout", idle.String())
		case "prepare-reset":
			stmt, err := f.owner.PrepareContext(f.p.Ctx, "SHOW idle_session_timeout")
			if err != nil {
				t.Fatal("Prepare did not reset ready boundary (O5)", err)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		case "ping-reset":
			if err := f.owner.PingContext(f.p.Ctx); err != nil {
				t.Fatal("Ping did not reset ready boundary (O5)", err)
			}
		}
	}
	// Exceed the selected budget, not just the original250ms: a larger
	// candidate must still detect an owner that never resets its deadline.
	if elapsed := time.Since(armed); elapsed <= idle {
		t.Fatalf("%s: activity sequence elapsed %s did not cross idle budget %s", kind, elapsed, idle)
	}
	if gap := time.Since(lastStart); gap >= 3*idle/4 {
		return gap
	}
	if settingsPhysical(t, f.owner) != identity {
		t.Fatal("activity replaced physical owner")
	}
	timeoutReferenceIdle(t, f.p.Ctx, idle+100*time.Millisecond)
	f.fatalProbe("57P05")
	return 0
}
